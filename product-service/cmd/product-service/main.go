package main

import (
	"context"
	"fmt"
	"os"
	"time"

	_ "go.uber.org/automaxprocs"

	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	pb "go-flashsale-mini-kafka-basic/proto/product/v1"
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
	cleanupTelemetry, err := telemetry.InitTelemetry(context.Background(), "product-service", jaegerEndpoint)
	if err != nil {
		panic(err)
	}
	defer cleanupTelemetry(context.Background())
	logger := log.With(log.NewStdLogger(os.Stdout),
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
		"service.id", "product-service",
		"service.name", "product-service",
		"service.version", "v1.0.0",
	)

	// Inisialisasi Database
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
		dbDSN = fmt.Sprintf("host=%s user=%s password=%s dbname=db_product port=%s sslmode=disable", dbHost, dbUser, dbPassword, dbPort)
	}
	db, err := sqlx.Connect("postgres", dbDSN)
	if err != nil {
		log.NewHelper(logger).Warnf("Gagal terhubung ke database (abaikan sementara karena scaffold): %v", err)
		// Kita biarkan jalan menggunakan nil db untuk demo dummy data jika postgres belum siap
	} else {
		// Konfigurasi TCP Connection Pool Postgres
		db.SetMaxOpenConns(100)
		db.SetMaxIdleConns(20)
		db.SetConnMaxLifetime(30 * time.Minute)
	}

	// Inject dependensi menggunakan Wire
	productServer, err := initApp(db, logger)
	if err != nil {
		panic(err)
	}

	productPort := os.Getenv("PRODUCT_SERVICE_PORT")
	if productPort == "" {
		productPort = "19001"
	}

	// Setup gRPC Server Kratos
	grpcServer := kratosgrpc.NewServer(
		kratosgrpc.Address(":" + productPort),
		kratosgrpc.Logger(logger),
		kratosgrpc.Middleware(tracing.Server(), telemetry.ServerMetrics()),
	)
	pb.RegisterProductServiceServer(grpcServer, productServer)

	// Jalankan Kratos Application Lifecycle
	app := kratos.New(
		kratos.Name("product-service"),
		kratos.Server(
			grpcServer,
		),
		kratos.Logger(logger),
	)

	if err := app.Run(); err != nil {
		panic(err)
	}
}
