package port

import (
	"context"

	"github.com/stretchr/testify/mock"
	"go-flashsale-mini-kafka-basic/order-service/internal/domain/model"
)

// MockOrderRepository adalah mock untuk OrderRepository menggunakan testify/mock.
type MockOrderRepository struct {
	mock.Mock
}

func (m *MockOrderRepository) CreateOrderIdempotent(ctx context.Context, order *model.Order, eventID string) (bool, error) {
	args := m.Called(ctx, order, eventID)
	return args.Bool(0), args.Error(1)
}

func (m *MockOrderRepository) UpdateOrderStatusIdempotent(ctx context.Context, orderID, status, eventID string) (bool, error) {
	args := m.Called(ctx, orderID, status, eventID)
	return args.Bool(0), args.Error(1)
}

func (m *MockOrderRepository) GetOrder(ctx context.Context, orderID string) (*model.Order, error) {
	args := m.Called(ctx, orderID)
	if args.Get(0) != nil {
		return args.Get(0).(*model.Order), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockOrderRepository) CancelOrderAndEmitEvent(ctx context.Context, order *model.Order, event *model.OrderCancelledEvent) error {
	args := m.Called(ctx, order, event)
	return args.Error(0)
}
