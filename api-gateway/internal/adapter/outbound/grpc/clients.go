package grpc

import (
	"context"
	"fmt"
	"time"

	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/sony/gobreaker"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"go-flashsale-mini-kafka-basic/api-gateway/internal/application/port"
	"go-flashsale-mini-kafka-basic/shared/pkg/resilience"
	inventoryv1 "go-flashsale-mini-kafka-basic/proto/inventory/v1"
	paymentv1 "go-flashsale-mini-kafka-basic/proto/payment/v1"
	productv1 "go-flashsale-mini-kafka-basic/proto/product/v1"
	authv1 "go-flashsale-mini-kafka-basic/proto/auth/v1"
	orderv1 "go-flashsale-mini-kafka-basic/proto/order/v1"
)

// callTimeout adalah batas waktu maksimum untuk setiap panggilan gRPC ke service downstream.
// Nilai ini harus lebih kecil dari timeout HTTP yang diterima dari client (misal: Nginx 10s).
const callTimeout = 3 * time.Second

// grpcClients mengenkapsulasi semua koneksi gRPC downstream beserta
// mekanisme resilience (circuit breaker) per-service.
type grpcClients struct {
	productClient   productv1.ProductServiceClient
	inventoryClient inventoryv1.InventoryServiceClient
	paymentClient   paymentv1.PaymentServiceClient
	authClient      authv1.AuthServiceClient
	orderClient     orderv1.OrderServiceClient

	// Circuit breaker terpisah per-service agar isolasi kegagalan terjaga.
	// Jika inventory CB terbuka, payment & product TIDAK terdampak.
	productCB   *gobreaker.CircuitBreaker
	inventoryCB *gobreaker.CircuitBreaker
	paymentCB   *gobreaker.CircuitBreaker
	authCB      *gobreaker.CircuitBreaker
	orderCB     *gobreaker.CircuitBreaker
}

// keepaliveParams mengatur agar koneksi idle tidak dianggap mati secara diam-diam.
var keepaliveParams = googlegrpc.WithKeepaliveParams(keepalive.ClientParameters{
	Time:                360 * time.Second, // Kirim keepalive ping setiap 6 menit jika ada stream aktif (menghindari GoAway "too_many_pings")
	Timeout:             20 * time.Second,  // Timeout jika tidak ada respons setelah 20 detik
	PermitWithoutStream: false,             // JANGAN kirim ping jika idle (menghindari GoAway "too_many_pings")
})

// NewGrpcClients membuat koneksi ke semua service downstream dengan:
//   - Timeout per-call (callTimeout)
//   - Keepalive untuk deteksi koneksi mati
//   - Circuit Breaker per-service (menggunakan sony/gobreaker)
//   - Tracing middleware (OpenTelemetry)
func NewGrpcClients(productEndpoint, inventoryEndpoint, paymentEndpoint, authEndpoint, orderEndpoint string) (
	port.ProductServiceClient, port.InventoryServiceClient, port.PaymentServiceClient, port.AuthServiceClient, port.OrderServiceClient, error,
) {
	dialOpts := []googlegrpc.DialOption{keepaliveParams}

	// Koneksi ke Product Service
	connProd, err := kratosgrpc.DialInsecure(
		context.Background(),
		kratosgrpc.WithEndpoint(productEndpoint),
		kratosgrpc.WithMiddleware(tracing.Client()),
		kratosgrpc.WithOptions(dialOpts...),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("gagal koneksi ke product-service: %w", err)
	}

	// Koneksi ke Inventory Service
	connInv, err := kratosgrpc.DialInsecure(
		context.Background(),
		kratosgrpc.WithEndpoint(inventoryEndpoint),
		kratosgrpc.WithMiddleware(tracing.Client()),
		kratosgrpc.WithOptions(dialOpts...),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("gagal koneksi ke inventory-service: %w", err)
	}

	// Koneksi ke Payment Service
	connPay, err := kratosgrpc.DialInsecure(
		context.Background(),
		kratosgrpc.WithEndpoint(paymentEndpoint),
		kratosgrpc.WithMiddleware(tracing.Client()),
		kratosgrpc.WithOptions(dialOpts...),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("gagal koneksi ke payment-service: %w", err)
	}

	// Koneksi ke Auth Service
	connAuth, err := kratosgrpc.DialInsecure(
		context.Background(),
		kratosgrpc.WithEndpoint(authEndpoint),
		kratosgrpc.WithMiddleware(tracing.Client()),
		kratosgrpc.WithOptions(dialOpts...),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("gagal koneksi ke auth-service: %w", err)
	}

	// Koneksi ke Order Service
	connOrder, err := kratosgrpc.DialInsecure(
		context.Background(),
		kratosgrpc.WithEndpoint(orderEndpoint),
		kratosgrpc.WithMiddleware(tracing.Client()),
		kratosgrpc.WithOptions(dialOpts...),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("gagal koneksi ke order-service: %w", err)
	}

	clients := &grpcClients{
		productClient:   productv1.NewProductServiceClient(connProd),
		inventoryClient: inventoryv1.NewInventoryServiceClient(connInv),
		paymentClient:   paymentv1.NewPaymentServiceClient(connPay),
		authClient:      authv1.NewAuthServiceClient(connAuth),
		orderClient:     orderv1.NewOrderServiceClient(connOrder),

		productCB:   resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("product-service")),
		inventoryCB: resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("inventory-service")),
		paymentCB:   resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("payment-service")),
		authCB:      resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("auth-service")),
		orderCB:     resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("order-service")),
	}

	return clients, clients, clients, clients, clients, nil
}

// ListFlashSaleProducts memanggil Product Service dengan circuit breaker + timeout.
func (c *grpcClients) ListFlashSaleProducts(ctx context.Context, page, perPage int32) (*productv1.ListFlashSaleProductsResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := c.productCB.Execute(func() (interface{}, error) {
		return c.productClient.ListFlashSaleProducts(callCtx, &productv1.ListFlashSaleProductsRequest{
			Page:    page,
			PerPage: perPage,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("product-service: %w", err)
	}
	return result.(*productv1.ListFlashSaleProductsResponse), nil
}

// ReserveStock memanggil Inventory Service dengan circuit breaker + timeout.
// TIDAK menggunakan retry karena operasi ini TIDAK idempoten dari sisi Redis Lua Script —
// retry bisa menyebabkan stok dipotong dua kali untuk event_id yang berbeda.
// Idempotency sudah dijaga oleh Redis Lua Script via idempotency_key.
func (c *grpcClients) ReserveStock(ctx context.Context, productID, userID, eventID string) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := c.inventoryCB.Execute(func() (interface{}, error) {
		return c.inventoryClient.ReserveStock(callCtx, &inventoryv1.ReserveStockRequest{
			ProductId:      productID,
			UserId:         userID,
			IdempotencyKey: eventID,
			Quantity:       1,
		})
	})
	if err != nil {
		// gobreaker.ErrOpenState berarti CB terbuka — service downstream sedang tidak sehat
		if err == gobreaker.ErrOpenState {
			return false, fmt.Errorf("inventory-service tidak tersedia sementara (circuit open): %w", err)
		}
		return false, fmt.Errorf("inventory-service: %w", err)
	}

	resp := result.(*inventoryv1.ReserveStockResponse)
	return resp.GetSuccess(), nil
}

// ProcessPayment memanggil Payment Service dengan circuit breaker + timeout.
func (c *grpcClients) ProcessPayment(ctx context.Context, orderID string, amount int64) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := c.paymentCB.Execute(func() (interface{}, error) {
		return c.paymentClient.ProcessPayment(callCtx, &paymentv1.ProcessPaymentRequest{
			OrderId: orderID,
			Amount:  amount,
		})
	})
	if err != nil {
		if err == gobreaker.ErrOpenState {
			return false, fmt.Errorf("payment-service tidak tersedia sementara (circuit open): %w", err)
		}
		return false, fmt.Errorf("payment-service: %w", err)
	}

	return result != nil, nil
}

func (c *grpcClients) Register(ctx context.Context, username, password string) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := c.authCB.Execute(func() (interface{}, error) {
		return c.authClient.Register(callCtx, &authv1.RegisterRequest{
			Username: username,
			Password: password,
		})
	})
	if err != nil {
		return false, fmt.Errorf("auth-service register: %w", err)
	}

	resp := result.(*authv1.RegisterResponse)
	return resp.GetSuccess(), nil
}

func (c *grpcClients) Login(ctx context.Context, username, password string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := c.authCB.Execute(func() (interface{}, error) {
		return c.authClient.Login(callCtx, &authv1.LoginRequest{
			Username: username,
			Password: password,
		})
	})
	if err != nil {
		return "", fmt.Errorf("auth-service login: %w", err)
	}

	resp := result.(*authv1.LoginResponse)
	return resp.GetAccessToken(), nil
}

// GetOrder memanggil Order Service dengan circuit breaker + timeout.
func (c *grpcClients) GetOrder(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := c.orderCB.Execute(func() (interface{}, error) {
		return c.orderClient.GetOrder(callCtx, &orderv1.GetOrderRequest{
			OrderId: orderID,
		})
	})
	if err != nil {
		if err == gobreaker.ErrOpenState {
			return nil, fmt.Errorf("order-service tidak tersedia sementara (circuit open): %w", err)
		}
		return nil, fmt.Errorf("order-service: %w", err)
	}

	return result.(*orderv1.GetOrderResponse), nil
}
