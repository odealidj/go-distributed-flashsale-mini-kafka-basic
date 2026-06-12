package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	kratoslog "github.com/go-kratos/kratos/v2/log"

	grpc_adapter "go-flashsale-mini-kafka-basic/auth-service/internal/adapter/inbound/grpc"
	"go-flashsale-mini-kafka-basic/auth-service/internal/adapter/outbound/postgres"
	"go-flashsale-mini-kafka-basic/auth-service/internal/application/usecase"
	pb "go-flashsale-mini-kafka-basic/proto/auth/v1"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
)

func main() {
	_ = godotenv.Load()
	logger := kratoslog.With(kratoslog.NewStdLogger(os.Stdout),
		"ts", kratoslog.DefaultTimestamp,
		"caller", kratoslog.DefaultCaller,
		"service.id", "auth-service",
		"service.name", "auth-service",
		"service.version", "v1.0.0",
	)

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

	cleanupTelemetry, err := telemetry.InitTelemetry(context.Background(), "auth-service", jaegerEndpoint)
	if err != nil {
		log.Fatalf("failed to init tracer: %v", err)
	}
	defer cleanupTelemetry(context.Background())

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	userRepo := postgres.NewUserRepository(db)
	
	authUsecase, err := usecase.NewAuthUsecase(userRepo)
	if err != nil {
		log.Fatalf("failed to init auth usecase (check RSA keys): %v", err)
	}

	authServer := grpc_adapter.NewAuthServer(authUsecase)

	port := os.Getenv("AUTH_SERVICE_PORT")
	if port == "" {
		port = "9004"
	}
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	
	pb.RegisterAuthServiceServer(grpcServer, authServer)
	reflection.Register(grpcServer)

	logger.Log(kratoslog.LevelInfo, "msg", "Auth Service starting on port "+port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
