package postgres

import (
	"context"
	"encoding/json"

	"github.com/jmoiron/sqlx"
	"go-flashsale-mini-kafka-basic/payment-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/payment-service/internal/domain/model"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
)

type paymentRepository struct {
	db *sqlx.DB
}

func NewPaymentRepository(db *sqlx.DB) port.PaymentRepository {
	return &paymentRepository{db: db}
}

func (r *paymentRepository) SavePaymentAndEmitEvent(ctx context.Context, payment *model.Payment, eventType string, event interface{}) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Simpan Payment
	_, err = tx.ExecContext(ctx, `
		INSERT INTO payments (id, order_id, amount, status)
		VALUES ($1, $2, $3, $4)
	`, payment.ID, payment.OrderID, payment.Amount, payment.Status)
	if err != nil {
		return err
	}

	// 2. Simpan Outbox Event
	payloadBytes, _ := json.Marshal(event)
	tracePayload := telemetry.ExtractTraceparent(ctx)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_messages (aggregate_id, aggregate_type, event_type, payload, trace_payload, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
	`, payment.OrderID, "order", eventType, string(payloadBytes), tracePayload)
	if err != nil {
		return err
	}

	return tx.Commit()
}
