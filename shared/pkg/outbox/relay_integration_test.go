//go:build integration

package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kgo"

	"go-flashsale-mini-kafka-basic/shared/pkg/outbox"
)

func setupPostgres(ctx context.Context, t *testing.T) (*postgres.PostgresContainer, *sqlx.DB) {
	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15-alpine"),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("user"),
		postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(10*time.Second)),
	)
	require.NoError(t, err)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sqlx.Connect("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE outbox_messages (
			id SERIAL PRIMARY KEY,
			aggregate_id VARCHAR(255) NOT NULL,
			aggregate_type VARCHAR(255) NOT NULL,
			event_type VARCHAR(255) NOT NULL,
			payload JSONB NOT NULL,
			trace_payload TEXT,
			status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
	`)
	require.NoError(t, err)

	return pgContainer, db
}

func setupKafka(ctx context.Context, t *testing.T) (*kafka.KafkaContainer, []string) {
	kafkaContainer, err := kafka.RunContainer(ctx,
		kafka.WithClusterID("test-cluster"),
		testcontainers.WithImage("docker.redpanda.com/redpandadata/redpanda:v23.2.28"),
	)
	require.NoError(t, err)

	brokers, err := kafkaContainer.Brokers(ctx)
	require.NoError(t, err)

	return kafkaContainer, brokers
}

func TestRelayWorker_PublishPendingToKafka(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pgC, db := setupPostgres(ctx, t)
	defer pgC.Terminate(context.Background())
	defer db.Close()

	kafkaC, brokers := setupKafka(ctx, t)
	defer kafkaC.Terminate(context.Background())

	topic := "test.outbox.topic"

	// 1. Setup Relay Worker
	logger := log.DefaultLogger
	worker, err := outbox.NewRelayWorker(db, brokers, logger)
	require.NoError(t, err)

	// 2. Setup Test Kafka Consumer
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup("test-group"),
	)
	require.NoError(t, err)
	defer cl.Close()

	// 3. Insert mock PENDING message
	_, err = db.Exec(`
		INSERT INTO outbox_messages (aggregate_id, aggregate_type, event_type, payload, trace_payload, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
	`, "agg-123", "Order", "OrderCreatedEvent", `{"id":"agg-123","amount":100}`, "trace-123")
	require.NoError(t, err)

	// 4. Start Relay Worker asynchronously
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	go worker.Start(workerCtx, topic)

	// 5. Consume from Kafka and Verify
	fetches := cl.PollFetches(ctx)
	errs := fetches.Errors()
	if len(errs) > 0 {
		t.Fatalf("Fetch errors: %v", errs)
	}

	records := fetches.Records()
	require.Len(t, records, 1, "Expected exactly 1 record from Kafka")

	record := records[0]
	assert.Equal(t, "Order", string(record.Key))
	assert.Equal(t, `{"id":"agg-123","amount":100}`, string(record.Value))

	var traceHeader string
	for _, h := range record.Headers {
		if h.Key == "traceparent" {
			traceHeader = string(h.Value)
			break
		}
	}
	assert.Equal(t, "trace-123", traceHeader)

	// 6. Verify DB status is updated to SENT
	// Allow small window for worker to execute the DB UPDATE query
	time.Sleep(500 * time.Millisecond)
	var status string
	err = db.QueryRow(`SELECT status FROM outbox_messages WHERE aggregate_id = 'agg-123'`).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, "SENT", status)
}
