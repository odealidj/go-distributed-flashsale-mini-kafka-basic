package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"

	"go-flashsale-mini-kafka-basic/order-service/internal/application/usecase"
	"go-flashsale-mini-kafka-basic/order-service/internal/domain/model"
	"go-flashsale-mini-kafka-basic/shared/pkg/resilience"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
)

// dlqTopic adalah topic Dead Letter Queue.
// Pesan yang gagal diproses setelah max retry dikirim ke sini untuk
// diinspeksi manual (tidak dibuang begitu saja).
const dlqTopic = "flashsale.order.dlq"

// Consumer adalah Kafka consumer untuk Order Service.
// Mendengarkan event dari Inventory dan Payment, lalu menjalankan Saga.
type Consumer struct {
	client    *kgo.Client
	dlqClient *kgo.Client // Client terpisah untuk DLQ agar tidak tercampur
	usecase   *usecase.OrderSagaUsecase
	logger    *log.Helper
	retry     resilience.RetryConfig
}

// NewKafkaConsumer membuat Consumer baru dengan:
//   - Koneksi ke Kafka dengan auto-commit dinonaktifkan (manual commit untuk at-least-once semantik)
//   - DLQ client terpisah untuk mengirim pesan gagal
//   - Retry config untuk pemrosesan yang gagal transient
func NewKafkaConsumer(brokers []string, groupID string, uc *usecase.OrderSagaUsecase, logger log.Logger) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics("flashsale.inventory.events", "flashsale.payment.events"),
		// DisableAutoCommit agar kita kontrol kapan offset di-commit
		// (hanya setelah pemrosesan sukses atau setelah dikirim ke DLQ)
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat Kafka consumer: %w", err)
	}

	dlqCl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		cl.Close()
		return nil, fmt.Errorf("gagal membuat DLQ Kafka client: %w", err)
	}

	return &Consumer{
		client:    cl,
		dlqClient: dlqCl,
		usecase:   uc,
		logger:    log.NewHelper(logger),
		// Retry 3x dengan backoff untuk kegagalan transient (misal: DB sementara down)
		retry: resilience.RetryConfig{
			MaxAttempts:     3,
			InitialInterval: 500 * time.Millisecond,
			MaxInterval:     5 * time.Second,
			Multiplier:      2.0,
			Jitter:          true,
		},
	}, nil
}

// Start menjalankan consumer loop hingga ctx dibatalkan.
func (c *Consumer) Start(ctx context.Context) {
	c.logger.Info("Order Service Kafka Consumer dimulai")
	defer c.client.Close()
	defer c.dlqClient.Close()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Order Service Kafka Consumer dihentikan")
			return
		default:
			fetches := c.client.PollFetches(ctx)
			if errs := fetches.Errors(); len(errs) > 0 {
				for _, ferr := range errs {
					c.logger.Errorf("Kafka poll error: topic=%s partition=%d err=%v",
						ferr.Topic, ferr.Partition, ferr.Err)
				}
				continue
			}

			fetches.EachRecord(func(record *kgo.Record) {
				c.processRecord(ctx, record)
			})

			// Commit offset hanya setelah semua record dalam batch diproses
			if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
				c.logger.Errorf("Gagal commit offset Kafka: %v", err)
			}
		}
	}
}

// processRecord memproses satu record Kafka dengan retry + DLQ fallback.
func (c *Consumer) processRecord(ctx context.Context, record *kgo.Record) {
	// Ekstrak trace context dari Kafka header
	var traceparent string
	for _, h := range record.Headers {
		if h.Key == "traceparent" {
			traceparent = string(h.Value)
			break
		}
	}

	ctxWithTrace := telemetry.InjectTraceparent(ctx, traceparent)
	ctxWithTrace, span := otel.Tracer("order-service-consumer").Start(ctxWithTrace, "ConsumeEvent "+record.Topic)
	defer span.End()

	// Coba proses dengan retry
	processErr := resilience.DoWithRetry(ctxWithTrace, c.retry, func(attempt int) error {
		if attempt > 1 {
			c.logger.Warnf("Retry proses event topic=%s offset=%d (attempt=%d)",
				record.Topic, record.Offset, attempt)
		}
		return c.dispatch(ctxWithTrace, record)
	})

	if processErr != nil {
		// Kirim ke DLQ — pesan tidak hilang, bisa diinspeksi dan di-replay manual
		c.logger.Errorf("Event gagal setelah semua retry, dikirim ke DLQ: topic=%s offset=%d err=%v",
			record.Topic, record.Offset, processErr)
		c.sendToDLQ(ctx, record, processErr)
	}
}

// dispatch merutekan event ke handler yang tepat berdasarkan topic.
func (c *Consumer) dispatch(ctx context.Context, record *kgo.Record) error {
	switch record.Topic {
	case "flashsale.inventory.events":
		var event model.StockReservedEvent
		c.logger.Infof("=== RAW PAYLOAD DARI INVENTORY === %s", string(record.Value))
		if err := json.Unmarshal(record.Value, &event); err != nil {
			// Payload tidak valid = permanent error, jangan retry
			return fmt.Errorf("payload StockReservedEvent tidak valid (permanent): %w", err)
		}
		c.logger.Infof("=== EVENT SETELAH UNMARSHAL === %+v", event)
		return c.usecase.HandleStockReserved(ctx, &event)

	case "flashsale.payment.events":
		// Karena payment service bisa kirim sukses atau gagal dalam topic yang sama, kita perlu cek isinya.
		// Cara yang lebih robust adalah dengan menggunakan CloudEvents atau menaruh tipe event di Header.
		// Namun untuk kesederhanaan, kita coba unmarshal ke struct map untuk cek tipe event.
		var raw map[string]interface{}
		if err := json.Unmarshal(record.Value, &raw); err != nil {
			return fmt.Errorf("payload payment events tidak valid (permanent): %w", err)
		}

		if reason, hasReason := raw["reason"]; hasReason && reason != "" {
			var event model.PaymentFailedEvent
			if err := json.Unmarshal(record.Value, &event); err != nil {
				return fmt.Errorf("payload PaymentFailedEvent tidak valid (permanent): %w", err)
			}
			return c.usecase.HandlePaymentFailed(ctx, &event)
		} else {
			var event model.PaymentCompletedEvent
			if err := json.Unmarshal(record.Value, &event); err != nil {
				return fmt.Errorf("payload PaymentCompletedEvent tidak valid (permanent): %w", err)
			}
			return c.usecase.HandlePaymentCompleted(ctx, &event)
		}

	default:
		return fmt.Errorf("topic tidak dikenal: %s (permanent)", record.Topic)
	}
}

// sendToDLQ mengirim record yang gagal ke Dead Letter Queue topic.
// DLQ record menyertakan metadata: original topic, error message, dan timestamp.
func (c *Consumer) sendToDLQ(ctx context.Context, original *kgo.Record, reason error) {
	dlqRecord := &kgo.Record{
		Topic: dlqTopic,
		Key:   original.Key,
		Value: original.Value,
		Headers: append(original.Headers,
			kgo.RecordHeader{Key: "dlq.original.topic", Value: []byte(original.Topic)},
			kgo.RecordHeader{Key: "dlq.error", Value: []byte(reason.Error())},
			kgo.RecordHeader{Key: "dlq.timestamp", Value: []byte(time.Now().Format(time.RFC3339))},
		),
	}

	if err := c.dlqClient.ProduceSync(ctx, dlqRecord).FirstErr(); err != nil {
		// Jika DLQ sendiri gagal, log sebagai CRITICAL — butuh intervensi manual
		c.logger.Errorf("CRITICAL: Gagal kirim ke DLQ! topic=%s offset=%d err=%v",
			original.Topic, original.Offset, err)
	} else {
		c.logger.Infof("Event dikirim ke DLQ: original_topic=%s offset=%d", original.Topic, original.Offset)
	}
}
