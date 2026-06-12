package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go-flashsale-mini-kafka-basic/order-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/order-service/internal/application/usecase"
	"go-flashsale-mini-kafka-basic/order-service/internal/domain/model"
)

func TestOrderSagaUsecase_HandleStockReserved(t *testing.T) {
	// Arrange
	mockRepo := new(port.MockOrderRepository)
	uc := usecase.NewOrderSagaUsecase(mockRepo)

	ctx := context.Background()
	event := &model.StockReservedEvent{
		EventID:   "evt-123",
		UserID:    "user-1",
		ProductID: "prod_1",
		Quantity:  1,
		Price:     1000,
	}

	mockRepo.On("CreateOrderIdempotent", ctx, mock.MatchedBy(func(o *model.Order) bool {
		return o.UserID == event.UserID && o.ProductID == event.ProductID && o.Status == "PENDING"
	}), event.EventID).Return(true, nil)

	// Act
	err := uc.HandleStockReserved(ctx, event)

	// Assert
	assert.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestOrderSagaUsecase_HandlePaymentCompleted(t *testing.T) {
	// Arrange
	mockRepo := new(port.MockOrderRepository)
	uc := usecase.NewOrderSagaUsecase(mockRepo)

	ctx := context.Background()
	event := &model.PaymentCompletedEvent{
		EventID: "evt-payment-1",
		OrderID: "order-1",
		Amount:  1000,
	}

	mockRepo.On("UpdateOrderStatusIdempotent", ctx, event.OrderID, "PAID", event.EventID).Return(true, nil)

	// Act
	err := uc.HandlePaymentCompleted(ctx, event)

	// Assert
	assert.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestOrderSagaUsecase_HandlePaymentFailed(t *testing.T) {
	// Arrange
	mockRepo := new(port.MockOrderRepository)
	uc := usecase.NewOrderSagaUsecase(mockRepo)

	ctx := context.Background()
	event := &model.PaymentFailedEvent{
		EventID: "evt-fail-1",
		OrderID: "order-1",
		Amount:  994,
		Reason:  "Payment declined by bank",
	}

	existingOrder := &model.Order{
		ID:        "order-1",
		ProductID: "prod_1",
		Quantity:  1,
		Status:    "PENDING",
	}

	mockRepo.On("GetOrder", ctx, event.OrderID).Return(existingOrder, nil)
	mockRepo.On("CancelOrderAndEmitEvent", ctx, existingOrder, mock.MatchedBy(func(ce *model.OrderCancelledEvent) bool {
		return ce.OrderID == existingOrder.ID && ce.Reason == event.Reason
	})).Return(nil)

	// Act
	err := uc.HandlePaymentFailed(ctx, event)

	// Assert
	assert.NoError(t, err)
	mockRepo.AssertExpectations(t)
}
