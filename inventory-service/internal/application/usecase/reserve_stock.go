package usecase

import (
	"context"
	"encoding/json"
	"errors"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter         = otel.Meter("inventory-service")
	checkoutTotal metric.Int64Counter
)

func init() {
	var err error
	checkoutTotal, err = meter.Int64Counter("flashsale_checkout_total", metric.WithDescription("Total checkout requests processed"))
	if err != nil {
		panic(err)
	}
}

type ReserveStockUsecase struct {
	redisPort  port.RedisPort
	outboxPort port.OutboxPort
}

func NewReserveStockUsecase(redis port.RedisPort, outbox port.OutboxPort) *ReserveStockUsecase {
	return &ReserveStockUsecase{
		redisPort:  redis,
		outboxPort: outbox,
	}
}

// Execute menjalankan Saga penguncian stok.
func (uc *ReserveStockUsecase) Execute(ctx context.Context, productID string, userID string, eventID string, quantity int) error {
	success, err := uc.redisPort.ReserveStock(ctx, productID, eventID, quantity)
	if err != nil {
		checkoutTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "failed_redis_error"), attribute.String("product_id", productID)))
		return err
	}
	if !success {
		checkoutTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "failed_out_of_stock"), attribute.String("product_id", productID)))
		return errors.New("stok habis atau event idempotency gagal")
	}

	payload := map[string]interface{}{
		"event_id":        eventID,
		"idempotency_key": eventID,
		"product_id":      productID,
		"user_id":         userID,
		"status":          "RESERVED",
		"quantity":        quantity,
	}
	payloadBytes, _ := json.Marshal(payload)

	err = uc.outboxPort.InsertOutbox(ctx, eventID, "Inventory", "StockReservedEvent", payloadBytes)
	if err != nil {
		checkoutTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "failed_outbox_error"), attribute.String("product_id", productID)))
		return errors.New("gagal menyimpan outbox: " + err.Error())
	}

	checkoutTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "success"), attribute.String("product_id", productID)))
	return nil
}
