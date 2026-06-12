package postgres

import (
	"context"

	"github.com/jmoiron/sqlx"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
)

type outboxRepo struct {
	db *sqlx.DB
}

func NewOutboxRepo(db *sqlx.DB) port.OutboxPort {
	return &outboxRepo{db: db}
}

func (r *outboxRepo) InsertOutbox(ctx context.Context, aggregateID string, aggregateType string, eventType string, payload []byte) error {
	tracePayload := telemetry.ExtractTraceparent(ctx)
	query := `
		INSERT INTO outbox_messages (aggregate_id, aggregate_type, event_type, payload, trace_payload, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
	`
	_, err := r.db.ExecContext(ctx, query, aggregateID, aggregateType, eventType, payload, tracePayload)
	return err
}

// IsOutboxExist mengecek apakah record outbox dengan aggregate_id tertentu sudah ada.
// Digunakan oleh ReconciliationJob: jika key ada di Redis tapi TIDAK ada di Postgres,
// berarti reservasi ini "bocor" dan stok harus dikembalikan.
func (r *outboxRepo) IsOutboxExist(ctx context.Context, aggregateID string) (bool, error) {
	var count int
	query := `SELECT COUNT(1) FROM outbox_messages WHERE aggregate_id = $1`
	err := r.db.QueryRowContext(ctx, query, aggregateID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
