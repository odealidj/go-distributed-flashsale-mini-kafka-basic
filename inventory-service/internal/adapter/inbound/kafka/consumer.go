package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/shared/pkg/resilience"
	"go-flashsale-mini-kafka-basic/shared/pkg/telemetry"
)

const dlqTopic = "flashsale.inventory.dlq"

// OrderCancelledEvent mencocokkan struktur dari Order Service
type OrderCancelledEvent struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
	Reason    string `json:"reason"`
}

type Consumer struct {
	client    *kgo.Client
	dlqClient *kgo.Client
	redisPort port.RedisPort
	logger    *log.Helper
	retry     resilience.RetryConfig
}

func NewKafkaConsumer(brokers []string, groupID string, redisPort port.RedisPort, logger log.Logger) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics("flashsale.order.events"),
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
		redisPort: redisPort,
		logger:    log.NewHelper(logger),
		retry: resilience.RetryConfig{
			MaxAttempts:     3,
			InitialInterval: 500 * time.Millisecond,
			MaxInterval:     5 * time.Second,
			Multiplier:      2.0,
			Jitter:          true,
		},
	}, nil
}

func (c *Consumer) Start(ctx context.Context) {
	c.logger.Info("Inventory Service Kafka Consumer dimulai")
	defer c.client.Close()
	defer c.dlqClient.Close()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Inventory Service Kafka Consumer dihentikan")
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

			if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
				c.logger.Errorf("Gagal commit offset Kafka: %v", err)
			}
		}
	}
}

func (c *Consumer) processRecord(ctx context.Context, record *kgo.Record) {
	var traceparent string
	for _, h := range record.Headers {
		if h.Key == "traceparent" {
			traceparent = string(h.Value)
			break
		}
	}

	ctxWithTrace := telemetry.InjectTraceparent(ctx, traceparent)
	ctxWithTrace, span := otel.Tracer("inventory-service-consumer").Start(ctxWithTrace, "ConsumeEvent "+record.Topic)
	defer span.End()

	processErr := resilience.DoWithRetry(ctxWithTrace, c.retry, func(attempt int) error {
		if attempt > 1 {
			c.logger.Warnf("Retry proses event topic=%s offset=%d (attempt=%d)",
				record.Topic, record.Offset, attempt)
		}
		return c.dispatch(ctxWithTrace, record)
	})

	if processErr != nil {
		c.logger.Errorf("Event gagal setelah semua retry, dikirim ke DLQ: topic=%s offset=%d err=%v",
			record.Topic, record.Offset, processErr)
		c.sendToDLQ(ctx, record, processErr)
	}
}

func (c *Consumer) dispatch(ctx context.Context, record *kgo.Record) error {
	switch record.Topic {
	case "flashsale.order.events":
		// Cek tipe payload, karena topic ini mungkin berisi tipe event lain.
		var raw map[string]interface{}
		if err := json.Unmarshal(record.Value, &raw); err != nil {
			return fmt.Errorf("payload invalid (permanent): %w", err)
		}

		if reason, hasReason := raw["reason"]; hasReason && reason != "" {
			var event OrderCancelledEvent
			if err := json.Unmarshal(record.Value, &event); err != nil {
				return fmt.Errorf("payload OrderCancelledEvent tidak valid (permanent): %w", err)
			}
			return c.handleOrderCancelled(ctx, &event)
		}
		
		// Event selain OrderCancelledEvent (misal OrderCreatedEvent) diabaikan oleh inventory.
		return nil
	default:
		return fmt.Errorf("topic tidak dikenal: %s (permanent)", record.Topic)
	}
}

func (c *Consumer) handleOrderCancelled(ctx context.Context, event *OrderCancelledEvent) error {
	// Refund stock ke Redis
	// Idempotency diselesaikan di dalam Lua script
	success, err := c.redisPort.RefundStock(ctx, event.ProductID, event.EventID, event.Quantity)
	if err != nil {
		return fmt.Errorf("gagal refund stok ke Redis: %w", err)
	}

	if !success {
		// Log saja, karena mungkin stock_key sudah expire (selesai masa promo)
		// Namun ini bukan error yang butuh DLQ
		c.logger.Warnf("RefundStock me-return false (kemungkinan idempotency key tidak ditemukan atau promosi selesai). OrderID: %s", event.OrderID)
	} else {
		c.logger.Infof("Stok berhasil direfund untuk product_id=%s, order_id=%s", event.ProductID, event.OrderID)
	}

	// TODO: Idealnya mengembalikan stok juga ke PostgreSQL inventory database jika kita memaintainnya secara sinkron.
	// Tetapi dalam arsitektur Flash Sale, Source of Truth utama saat event berlangsung adalah Redis.
	return nil
}

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
		c.logger.Errorf("CRITICAL: Gagal kirim ke DLQ! topic=%s offset=%d err=%v",
			original.Topic, original.Offset, err)
	} else {
		c.logger.Infof("Event dikirim ke DLQ: original_topic=%s offset=%d", original.Topic, original.Offset)
	}
}
