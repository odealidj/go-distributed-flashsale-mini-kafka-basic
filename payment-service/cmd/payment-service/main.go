package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	_ "go.uber.org/automaxprocs"

	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
	"github.com/go-kratos/kratos/v2/middleware/tracing"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	pb "go-flashsale-mini-kafka-basic/proto/payment/v1"
	"go-flashsale-mini-kafka-basic/shared/pkg/outbox"
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
	cleanupTelemetry, err := telemetry.InitTelemetry(context.Background(), "payment-service", jaegerEndpoint)
	if err != nil {
		panic(err)
	}
	defer cleanupTelemetry(context.Background())
	logger := log.With(log.NewStdLogger(os.Stdout),
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
		"service.id", "payment-service",
		"service.name", "payment-service",
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
		dbDSN = fmt.Sprintf("host=%s user=%s password=%s dbname=db_payment port=%s sslmode=disable", dbHost, dbUser, dbPassword, dbPort)
	}
	db, err := sqlx.Connect("postgres", dbDSN)
	if err != nil {
		panic(err)
	}
	// Konfigurasi TCP Connection Pool Postgres
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(20)
	db.SetConnMaxLifetime(30 * time.Minute)

	// 2. Setup Server menggunakan Wire
	paymentServer := InitializePaymentServer(db)

	paymentPort := os.Getenv("PAYMENT_SERVICE_PORT")
	if paymentPort == "" {
		paymentPort = "19003"
	}

	// 3. Setup gRPC Server Kratos
	grpcServer := kratosgrpc.NewServer(
		kratosgrpc.Address(":" + paymentPort),
		kratosgrpc.Logger(logger),
		kratosgrpc.Middleware(tracing.Server(), telemetry.ServerMetrics()),
	)
	pb.RegisterPaymentServiceServer(grpcServer, paymentServer)

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

	// 4. Jalankan Outbox Relay Worker di Background
	relay, err := outbox.NewRelayWorker(db, kafkaBrokers, logger)
	if err == nil {
		go relay.Start(context.Background(), "flashsale.payment.events")
	} else {
		log.Errorf("Failed to start outbox relay: %v", err)
	}

	// 5. Jalankan App
	app := kratos.New(
		kratos.Name("payment-service"),
		kratos.Server(
			grpcServer,
		),
		kratos.Logger(logger),
	)

	if err := app.Run(); err != nil {
		panic(err)
	}
}
