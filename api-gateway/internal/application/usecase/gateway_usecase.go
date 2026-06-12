package usecase

import (
	"context"

	"github.com/google/uuid"
	"go-flashsale-mini-kafka-basic/api-gateway/internal/application/port"
	productv1 "go-flashsale-mini-kafka-basic/proto/product/v1"
	orderv1 "go-flashsale-mini-kafka-basic/proto/order/v1"
)

type GatewayUsecase struct {
	productClient   port.ProductServiceClient
	inventoryClient port.InventoryServiceClient
	paymentClient   port.PaymentServiceClient
	authClient      port.AuthServiceClient
	orderClient     port.OrderServiceClient
}

func NewGatewayUsecase(p port.ProductServiceClient, i port.InventoryServiceClient, pay port.PaymentServiceClient, auth port.AuthServiceClient, order port.OrderServiceClient) *GatewayUsecase {
	return &GatewayUsecase{
		productClient:   p,
		inventoryClient: i,
		paymentClient:   pay,
		authClient:      auth,
		orderClient:     order,
	}
}

func (uc *GatewayUsecase) GetProducts(ctx context.Context, page, perPage int32) (*productv1.ListFlashSaleProductsResponse, error) {
	return uc.productClient.ListFlashSaleProducts(ctx, page, perPage)
}

func (uc *GatewayUsecase) Checkout(ctx context.Context, userID, productID string, idempKey string) (string, bool, error) {
	// 1. Gunakan Idempotency Key dari client atau generate UUID baru jika kosong
	eventID := idempKey
	if eventID == "" {
		eventID = uuid.New().String()
	}

	// 2. Hubungi Inventory Service untuk reservasi stok secara synchronous
	// Jika berhasil, Inventory akan emit event Kafka ke Order Service secara asynchronous
	success, err := uc.inventoryClient.ReserveStock(ctx, productID, userID, eventID)
	
	return eventID, success, err
}

func (uc *GatewayUsecase) ProcessPayment(ctx context.Context, orderID string, amount int64) (bool, error) {
	return uc.paymentClient.ProcessPayment(ctx, orderID, amount)
}

func (uc *GatewayUsecase) Register(ctx context.Context, username, password string) (bool, error) {
	return uc.authClient.Register(ctx, username, password)
}

func (uc *GatewayUsecase) Login(ctx context.Context, username, password string) (string, error) {
	return uc.authClient.Login(ctx, username, password)
}

func (uc *GatewayUsecase) GetOrder(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
	return uc.orderClient.GetOrder(ctx, orderID)
}
