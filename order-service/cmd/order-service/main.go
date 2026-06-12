package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	_ "go.uber.org/automaxprocs"

	"go-flashsale-mini-kafka-basic/shared/pkg/outbox"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"go-flashsale-mini-kafka-basic/order-service/internal/adapter/inbound/kafka"
	"go-flashsale-mini-kafka-basic/order-service/internal/adapter/inbound/grpc"
	"go-flashsale-mini-kafka-basic/order-service/internal/adapter/outbound/postgres"
	"go-flashsale-mini-kafka-basic/order-service/internal/application/usecase"
	"go-flashsale-mini-kafka-basic/order-service/internal/application/worker"
	pb "go-flashsale-mini-kafka-basic/proto/order/v1"

	"github.com/go-kratos/kratos/v2"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
)

func main() {
	// Construct Jaeger OTLP Endpoint
	jaegerEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if jaegerEndpoint == "" {
		jaegerHost := os.Getenv("JAEGER_HOST")
		if jaegerHost == "" {
			jaegerHost = "localhost"
		}
		jaegerPort := os.Getenv("JAEGER_OTLP_GRPC_PORT")
		if jaegerPort == "" {
			jaegerPort = "14317"
		}
		jaegerEndpoint = jaegerHost + ":" + jaegerPort
	}

	// Init Tracer
	cleanupTelemetry, err := telemetry.InitTelemetry(context.Background(), "order-service", jaegerEndpoint)
	if err != nil {
		panic(err)
	}
	defer cleanupTelemetry(context.Background())
	logger := log.With(log.NewStdLogger(os.Stdout),
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
		"service.id", "order-service",
		"service.name", "order-service",
		"service.version", "v1.0.0",
	)

	// 1. Setup Postgres
	dbDSN := os.Getenv("DATABASE_URL")
	if dbDSN == "" {
		dbHost := os.Getenv("DB_HOST")
		if dbHost == "" {
			dbHost = "localhost"
		}
		dbUser := os.Getenv("POSTGRES_USER")
		if dbUser == "" {
			dbUser = "root"
		}
		dbPassword := os.Getenv("POSTGRES_PASSWORD")
		if dbPassword == "" {
			dbPassword = "rootpassword"
		}
		dbPort := os.Getenv("DB_PORT")
		if dbPort == "" {
			dbPort = "15432"
		}
		dbDSN = fmt.Sprintf("host=%s user=%s password=%s dbname=db_order port=%s sslmode=disable", dbHost, dbUser, dbPassword, dbPort)
	}
	db, err := sqlx.Connect("postgres", dbDSN)
	if err != nil {
		panic(err)
	}
	// Konfigurasi TCP Connection Pool Postgres
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(20)
	db.SetConnMaxLifetime(30 * time.Minute)

	var redisClient *redis.Client
	sentinelAddrs := os.Getenv("REDIS_SENTINEL_ADDRS")
	if sentinelAddrs != "" {
		masterName := os.Getenv("REDIS_MASTER_NAME")
		if masterName == "" {
			masterName = "mymaster"
		}
		redisClient = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    masterName,
			SentinelAddrs: strings.Split(sentinelAddrs, ","),
		})
	} else {
		redisClient = redis.NewClient(&redis.Options{
			Addr: os.Getenv("REDIS_ADDR"),
		})
	}

	// 2. Init Dependencies Manual (tanpa Wire agar cepat untuk worker)
	repo := postgres.NewOrderRepository(db, logger)
	uc := usecase.NewOrderSagaUsecase(repo, redisClient)

	// Construct Kafka Brokers
	kafkaBrokersStr := os.Getenv("KAFKA_BROKERS")
	var kafkaBrokers []string
	if kafkaBrokersStr != "" {
		kafkaBrokers = strings.Split(kafkaBrokersStr, ",")
	} else {
		kafkaHost := os.Getenv("KAFKA_HOST")
		if kafkaHost == "" {
			kafkaHost = "localhost"
		}
		kafkaPort := os.Getenv("KAFKA_EXTERNAL_PORT")
		if kafkaPort == "" {
			kafkaPort = "19094"
		}
		kafkaBrokers = []string{fmt.Sprintf("%s:%s", kafkaHost, kafkaPort)}
	}

	consumer, err := kafka.NewKafkaConsumer(kafkaBrokers, "order-service-group", uc, logger)
	if err != nil {
		panic(err)
	}

	// 3. Jalankan Kafka Consumer & Timeout Worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	timeoutWorker := worker.NewTimeoutWorker(db, repo, logger)

	// Jalankan Outbox Relay Worker di Background
	relay, err := outbox.NewRelayWorker(db, kafkaBrokers, logger)
	if err == nil {
		go relay.Start(ctx, "flashsale.order.events")
	} else {
		log.Errorf("Failed to start outbox relay: %v", err)
	}

	go consumer.Start(ctx)
	go timeoutWorker.Start(ctx)

	// 4. Setup gRPC
	orderPort := os.Getenv("ORDER_SERVICE_PORT")
	if orderPort == "" {
		orderPort = "9005"
	}

	grpcServer := kratosgrpc.NewServer(
		kratosgrpc.Address(":" + orderPort),
		kratosgrpc.Logger(logger),
		kratosgrpc.Middleware(tracing.Server(), telemetry.ServerMetrics()),
	)

	orderServer := grpc.NewOrderServer(repo)
	pb.RegisterOrderServiceServer(grpcServer, orderServer)

	// 5. Jalankan App
	app := kratos.New(
		kratos.Name("order-service"),
		kratos.Server(
			grpcServer,
		),
		kratos.Logger(logger),
	)

	if err := app.Run(); err != nil {
		panic(err)
	}

	log.Info("Shutting down Order Service...")
}
