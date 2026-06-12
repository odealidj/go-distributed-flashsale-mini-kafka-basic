package postgres

import (
	"context"
	"encoding/json"

	"github.com/jmoiron/sqlx"
	"github.com/go-kratos/kratos/v2/log"

	"go-flashsale-mini-kafka-basic/order-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/order-service/internal/domain/model"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
)

type orderRepository struct {
	db     *sqlx.DB
	logger *log.Helper
}

func NewOrderRepository(db *sqlx.DB, logger log.Logger) port.OrderRepository {
	return &orderRepository{
		db:     db,
		logger: log.NewHelper(logger),
	}
}

func (r *orderRepository) CreateOrderIdempotent(ctx context.Context, order *model.Order, eventID string) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// 1. Cek Idempotency
	var exists bool
	err = tx.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id=$1)", eventID)
	if err != nil {
		return false, err
	}
	if exists {
		r.logger.Infof("Event %s already processed. Skipping create order.", eventID)
		return false, nil
	}

	// 2. Insert Order
	_, err = tx.ExecContext(ctx, `
		INSERT INTO orders (id, user_id, product_id, quantity, total_amount, status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, order.ID, order.UserID, order.ProductID, order.Quantity, order.TotalAmount, order.Status)
	if err != nil {
		return false, err
	}

	// 3. Insert ke processed_events
	_, err = tx.ExecContext(ctx, "INSERT INTO processed_events (event_id) VALUES ($1)", eventID)
	if err != nil {
		return false, err
	}

	r.logger.Infof("Order %s created successfully for event %s", order.ID, eventID)
	return true, tx.Commit()
}

func (r *orderRepository) UpdateOrderStatusIdempotent(ctx context.Context, orderID, status, eventID string) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// 1. Cek Idempotency
	var exists bool
	err = tx.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id=$1)", eventID)
	if err != nil {
		return false, err
	}
	if exists {
		r.logger.Infof("Event %s already processed. Skipping update order status.", eventID)
		return false, nil
	}

	// 2. Update Order
	_, err = tx.ExecContext(ctx, "UPDATE orders SET status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2", status, orderID)
	if err != nil {
		return false, err
	}

	// 3. Insert ke processed_events
	_, err = tx.ExecContext(ctx, "INSERT INTO processed_events (event_id) VALUES ($1)", eventID)
	if err != nil {
		return false, err
	}

	r.logger.Infof("Order %s status updated to %s for event %s", orderID, status, eventID)
	return true, tx.Commit()
}

func (r *orderRepository) GetOrder(ctx context.Context, orderID string) (*model.Order, error) {
	var order model.Order
	err := r.db.GetContext(ctx, &order, "SELECT id, user_id, product_id, quantity, total_amount, status, created_at, updated_at FROM orders WHERE id = $1", orderID)
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *orderRepository) CancelOrderAndEmitEvent(ctx context.Context, order *model.Order, event *model.OrderCancelledEvent) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Cek Idempotency (optional, but good if triggered by an event. Here eventID from event itself)
	var exists bool
	err = tx.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id=$1)", event.EventID)
	if err != nil {
		return err
	}
	if exists {
		r.logger.Infof("Cancel event %s already processed. Skipping cancel.", event.EventID)
		return nil
	}

	// 2. Update Order
	_, err = tx.ExecContext(ctx, "UPDATE orders SET status = 'CANCELLED', updated_at = CURRENT_TIMESTAMP WHERE id = $1 AND status = 'PENDING'", order.ID)
	if err != nil {
		return err
	}

	// 3. Simpan Outbox Event
	payloadBytes, _ := json.Marshal(event)
	tracePayload := telemetry.ExtractTraceparent(ctx)
	
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_messages (aggregate_id, aggregate_type, event_type, payload, trace_payload, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
	`, order.ID, "order", "OrderCancelledEvent", string(payloadBytes), tracePayload)
	if err != nil {
		return err
	}

	r.logger.Infof("Order %s cancelled and OrderCancelledEvent emitted", order.ID)
	return tx.Commit()
}
