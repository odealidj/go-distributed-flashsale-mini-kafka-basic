package main

import (
	"context"
	"os"
	"strings"
	"time"

	_ "go.uber.org/automaxprocs"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"go-flashsale-mini-kafka-basic/api-gateway/internal/adapter/inbound/rest"
	"go-flashsale-mini-kafka-basic/api-gateway/internal/adapter/outbound/grpc"
	"go-flashsale-mini-kafka-basic/api-gateway/internal/application/usecase"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
	
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/handlers"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := log.With(log.NewStdLogger(os.Stdout),
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
		"service.id", "api-gateway",
		"service.name", "api-gateway",
		"service.version", "v1.0.0",
	)

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
	cleanupTelemetry, err := telemetry.InitTelemetry(context.Background(), "api-gateway", jaegerEndpoint)
	if err != nil {
		panic(err)
	}
	defer cleanupTelemetry(context.Background())

	productEndpoint := os.Getenv("PRODUCT_SERVICE_ENDPOINT")
	if productEndpoint == "" {
		productEndpoint = "localhost:19001"
	}
	inventoryEndpoint := os.Getenv("INVENTORY_SERVICE_ENDPOINT")
	if inventoryEndpoint == "" {
		inventoryEndpoint = "localhost:19002"
	}
	paymentEndpoint := os.Getenv("PAYMENT_SERVICE_ENDPOINT")
	if paymentEndpoint == "" {
		paymentEndpoint = "localhost:19003"
	}
	authEndpoint := os.Getenv("AUTH_SERVICE_ENDPOINT")
	if authEndpoint == "" {
		authEndpoint = "localhost:19004"
	}
	orderEndpoint := os.Getenv("ORDER_SERVICE_ENDPOINT")
	if orderEndpoint == "" {
		orderEndpoint = "localhost:19005" // Port 9005 for order-service, mapped to 19005 internally
	}

	prodClient, invClient, payClient, authClient, orderClient, err := grpc.NewGrpcClients(productEndpoint, inventoryEndpoint, paymentEndpoint, authEndpoint, orderEndpoint)
	if err != nil {
		panic(err)
	}

	uc := usecase.NewGatewayUsecase(prodClient, invClient, payClient, authClient, orderClient)

	apiGatewayPort := os.Getenv("API_GATEWAY_PORT")
	if apiGatewayPort == "" {
		apiGatewayPort = "18000"
	}
	
	keyPath := os.Getenv("JWT_PUBLIC_KEY_PATH")
	if keyPath == "" {
		panic("JWT_PUBLIC_KEY_PATH is not set")
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		panic(err)
	}
	publicKey, err := jwt.ParseRSAPublicKeyFromPEM(keyBytes)
	if err != nil {
		panic(err)
	}

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

	httpSrv := kratoshttp.NewServer(
		kratoshttp.Address(":" + apiGatewayPort),
		kratoshttp.Logger(logger),
		kratoshttp.Timeout(60 * time.Second),
		kratoshttp.Middleware(tracing.Server(), telemetry.ServerMetrics()),
		kratoshttp.Filter(
			telemetry.ServerHTTPMetricsFilter(),
			handlers.CORS(
				handlers.AllowedOrigins([]string{"*"}),
				handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}),
				handlers.AllowedHeaders([]string{"Content-Type", "Authorization", "X-Idempotency-Key", "accept"}),
			),
		),
	)
	rest.RegisterHTTPServer(httpSrv, uc, logger, publicKey, redisClient)

	app := kratos.New(
		kratos.Name("api-gateway"),
		kratos.Server(httpSrv),
		kratos.Logger(logger),
	)

	if err := app.Run(); err != nil {
		panic(err)
	}
}
