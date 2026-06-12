package usecase

import (
	"context"
	"github.com/google/uuid"

	"github.com/redis/go-redis/v9"
	"go-flashsale-mini-kafka-basic/order-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/order-service/internal/domain/model"
)

type OrderSagaUsecase struct {
	repo port.OrderRepository
	rdb  *redis.Client
}

func NewOrderSagaUsecase(repo port.OrderRepository, rdb *redis.Client) *OrderSagaUsecase {
	return &OrderSagaUsecase{repo: repo, rdb: rdb}
}

// HandleStockReserved dipanggil saat ada event StockReservedEvent dari Kafka
func (uc *OrderSagaUsecase) HandleStockReserved(ctx context.Context, event *model.StockReservedEvent) error {
	// Gunakan IdempotencyKey dari client sebagai order_id agar client bisa langsung pakai untuk /pay
	orderID := event.IdempotencyKey
	if orderID == "" {
		orderID = uuid.New().String() // fallback jika key tidak ada (backward compat)
	}
	order := &model.Order{
		ID:          orderID,
		UserID:      event.UserID,
		ProductID:   event.ProductID,
		Quantity:    event.Quantity,
		TotalAmount: int64(event.Quantity) * event.Price,
		Status:      "PENDING",
	}

	_, err := uc.repo.CreateOrderIdempotent(ctx, order, event.EventID)
	if err == nil {
		uc.rdb.Publish(ctx, "order:status:"+orderID, "PENDING")
	}
	return err
}

// HandlePaymentCompleted dipanggil saat ada event PaymentCompletedEvent dari Kafka
func (uc *OrderSagaUsecase) HandlePaymentCompleted(ctx context.Context, event *model.PaymentCompletedEvent) error {
	_, err := uc.repo.UpdateOrderStatusIdempotent(ctx, event.OrderID, "PAID", event.EventID)
	if err == nil {
		uc.rdb.Publish(ctx, "order:status:"+event.OrderID, "PAID")
	}
	return err
}

// HandlePaymentFailed dipanggil saat ada event PaymentFailedEvent dari Kafka
func (uc *OrderSagaUsecase) HandlePaymentFailed(ctx context.Context, event *model.PaymentFailedEvent) error {
	order, err := uc.repo.GetOrder(ctx, event.OrderID)
	if err != nil {
		return err
	}

	cancelEvent := &model.OrderCancelledEvent{
		EventID:   event.EventID, // Gunakan eventID dari payment failed sebagai idempotency key
		OrderID:   order.ID,
		ProductID: order.ProductID,
		Quantity:  order.Quantity,
		Reason:    event.Reason,
	}

	err = uc.repo.CancelOrderAndEmitEvent(ctx, order, cancelEvent)
	if err == nil {
		uc.rdb.Publish(ctx, "order:status:"+order.ID, "CANCELLED")
	}
	return err
}
