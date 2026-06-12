package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/jmoiron/sqlx"
	"github.com/twmb/franz-go/pkg/kgo"

	"go-flashsale-mini-kafka-basic/shared/pkg/resilience"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter      = otel.Meter("outbox-relay")
	relayTotal metric.Int64Counter
)

func init() {
	var err error
	relayTotal, err = meter.Int64Counter(
		"flashsale_outbox_relay_total",
		metric.WithDescription("Total outbox messages relayed"),
	)
	if err != nil {
		panic(err)
	}
}

// OutboxMessage merepresentasikan satu baris dari tabel outbox_messages.
type OutboxMessage struct {
	ID            int    `db:"id"`
	AggregateID   string `db:"aggregate_id"`
	AggregateType string `db:"aggregate_type"`
	EventType     string `db:"event_type"`
	Payload       string `db:"payload"`
	TracePayload  string `db:"trace_payload"`
}

// RelayWorker adalah komponen yang secara periodik membaca pesan dari tabel
// outbox_messages di PostgreSQL dan mempublishnya ke Kafka.
type RelayWorker struct {
	db     *sqlx.DB
	client *kgo.Client
	logger *log.Helper
	retry  resilience.RetryConfig
}

// NewRelayWorker membuat RelayWorker baru yang terhubung ke PostgreSQL dan Kafka.
func NewRelayWorker(db *sqlx.DB, kafkaBrokers []string, logger log.Logger) (*RelayWorker, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal inisialisasi Kafka client: %w", err)
	}

	return &RelayWorker{
		db:     db,
		client: cl,
		logger: log.NewHelper(logger),
		retry: resilience.RetryConfig{
			MaxAttempts:     5,
			InitialInterval: 200 * time.Millisecond,
			MaxInterval:     10 * time.Second,
			Multiplier:      2.0,
			Jitter:          true,
		},
	}, nil
}

// Start menjalankan polling loop hingga ctx dibatalkan.
func (w *RelayWorker) Start(ctx context.Context, topic string) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	defer w.client.Close()

	w.logger.Infof("Outbox Relay Worker dimulai untuk topic: %s", topic)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Outbox Relay Worker dihentikan")
			return
		case <-ticker.C:
			w.processPendingMessages(ctx, topic)
		}
	}
}

// processPendingMessages memproses batch pesan PENDING dari outbox_messages.
func (w *RelayWorker) processPendingMessages(ctx context.Context, topic string) {
	tx, err := w.db.BeginTxx(ctx, nil)
	if err != nil {
		w.logger.Errorf("Gagal membuka transaksi outbox: %v", err)
		return
	}
	defer tx.Rollback()

	var msgs []OutboxMessage
	query := `
		WITH messages AS (
			SELECT id, aggregate_id, aggregate_type, event_type, payload, COALESCE(trace_payload, '') as trace_payload
			FROM outbox_messages
			WHERE status = 'PENDING'
			ORDER BY created_at ASC, id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_messages om
		SET status = 'PROCESSING'
		FROM messages m
		WHERE om.id = m.id
		RETURNING m.id, m.aggregate_id, m.aggregate_type, m.event_type, m.payload, m.trace_payload
		`
	err = tx.SelectContext(ctx, &msgs, query, 50)
	if err != nil {
		w.logger.Errorf("Gagal polling outbox messages: %v", err)
		return
	}

	if len(msgs) == 0 {
		tx.Rollback()
		return
	}

	for _, msg := range msgs {
		publishErr := resilience.DoWithRetry(ctx, w.retry, func(attempt int) error {
			if attempt > 1 {
				w.logger.Warnf("Retry publish event id=%d (attempt=%d)", msg.ID, attempt)
			}

			record := &kgo.Record{
				Topic: topic,
				Key:   []byte(msg.AggregateID),
				Value: []byte(msg.Payload),
			}

			if msg.TracePayload != "" {
				record.Headers = append(record.Headers, kgo.RecordHeader{
					Key:   "traceparent",
					Value: []byte(msg.TracePayload),
				})
			}

			return w.client.ProduceSync(ctx, record).FirstErr()
		})

		if publishErr != nil {
			w.logger.Errorf("Gagal publish event id=%d setelah semua retry: %v", msg.ID, publishErr)
			_, _ = tx.ExecContext(ctx,
				"UPDATE outbox_messages SET status = 'FAILED' WHERE id = $1",
				msg.ID,
			)
			relayTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "failed"), attribute.String("event_type", msg.EventType)))
			continue
		}

		if _, err = tx.ExecContext(ctx,
			"UPDATE outbox_messages SET status = 'SENT' WHERE id = $1",
			msg.ID,
		); err != nil {
			w.logger.Errorf("Gagal update status SENT untuk id=%d: %v", msg.ID, err)
		} else {
			w.logger.Infof("Event %s (id=%d) berhasil dikirim ke Kafka", msg.EventType, msg.ID)
			relayTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "sent"), attribute.String("event_type", msg.EventType)))
		}
	}

	if err = tx.Commit(); err != nil {
		w.logger.Errorf("Gagal commit transaksi outbox: %v", err)
	}
}
