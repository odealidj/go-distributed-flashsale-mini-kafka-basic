package port

import (
	"context"
	productv1 "go-flashsale-mini-kafka-basic/proto/product/v1"
	orderv1 "go-flashsale-mini-kafka-basic/proto/order/v1"
)

type ProductServiceClient interface {
	ListFlashSaleProducts(ctx context.Context, page, perPage int32) (*productv1.ListFlashSaleProductsResponse, error)
}

type InventoryServiceClient interface {
	ReserveStock(ctx context.Context, productID, userID, eventID string) (bool, error)
}

type PaymentServiceClient interface {
	ProcessPayment(ctx context.Context, orderID string, amount int64) (bool, error)
}

type AuthServiceClient interface {
	Register(ctx context.Context, username, password string) (bool, error)
	Login(ctx context.Context, username, password string) (string, error)
}

type OrderServiceClient interface {
	GetOrder(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error)
}
