//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	redismodule "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/twmb/franz-go/pkg/kgo"

	appkafka "go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/inbound/kafka"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/outbound/redis"
)

func setupRedis(ctx context.Context, t *testing.T) (*redismodule.RedisContainer, *goredis.Client) {
	redisC, err := redismodule.RunContainer(ctx,
		testcontainers.WithImage("redis:7-alpine"),
	)
	require.NoError(t, err)

	endpoint, err := redisC.ConnectionString(ctx)
	require.NoError(t, err)

	opts, err := goredis.ParseURL(endpoint)
	require.NoError(t, err)

	rdb := goredis.NewClient(opts)
	err = rdb.Ping(ctx).Err()
	require.NoError(t, err)

	return redisC, rdb
}

func setupKafka(ctx context.Context, t *testing.T) (*kafka.KafkaContainer, []string) {
	kafkaC, err := kafka.RunContainer(ctx,
		kafka.WithClusterID("test-cluster"),
		testcontainers.WithImage("docker.redpanda.com/redpandadata/redpanda:v23.2.28"),
	)
	require.NoError(t, err)

	brokers, err := kafkaC.Brokers(ctx)
	require.NoError(t, err)

	return kafkaC, brokers
}

func TestInventoryConsumer_HandleOrderCancelledEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 1. Setup Redis
	redisC, rdb := setupRedis(ctx, t)
	defer redisC.Terminate(context.Background())
	defer rdb.Close()

	// Setup initial stock in Redis
	productID := "prod_1"
	stockKey := "stock:" + productID
	err := rdb.Set(ctx, stockKey, 10, 0).Err()
	require.NoError(t, err)

	// 2. Setup Kafka
	kafkaC, brokers := setupKafka(ctx, t)
	defer kafkaC.Terminate(context.Background())

	// 3. Init RedisPort and Kafka Consumer
	redisPort := redis.NewRedisPort(rdb)
	logger := log.DefaultLogger
	consumer, err := appkafka.NewKafkaConsumer(brokers, "test-inventory-group", redisPort, logger)
	require.NoError(t, err)

	// 4. Start Consumer asynchronously
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	go consumer.Start(consumerCtx)

	// 5. Produce OrderCancelledEvent to Kafka
	producer, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)
	defer producer.Close()

	event := map[string]interface{}{
		"event_id":   "evt-cancel-123",
		"order_id":   "ord-999",
		"product_id": productID,
		"quantity":   2,
		"reason":     "payment failed",
	}
	payload, _ := json.Marshal(event)

	record := &kgo.Record{
		Topic: "flashsale.order.events",
		Key:   []byte("Order"),
		Value: payload,
	}
	err = producer.ProduceSync(ctx, record).FirstErr()
	require.NoError(t, err)

	// 6. Wait for consumer to process (poll Redis until stock increases)
	assert.Eventually(t, func() bool {
		val, err := rdb.Get(ctx, stockKey).Int()
		if err != nil {
			return false
		}
		// Expecting stock to be 10 + 2 = 12
		return val == 12
	}, 10*time.Second, 500*time.Millisecond, "Stock should be refunded to 12")
}

func TestInventoryConsumer_InvalidPayload_SendsToDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	redisC, rdb := setupRedis(ctx, t)
	defer redisC.Terminate(context.Background())

	kafkaC, brokers := setupKafka(ctx, t)
	defer kafkaC.Terminate(context.Background())

	redisPort := redis.NewRedisPort(rdb)
	consumer, err := appkafka.NewKafkaConsumer(brokers, "test-inventory-group", redisPort, log.DefaultLogger)
	require.NoError(t, err)

	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	go consumer.Start(consumerCtx)

	// Produce invalid payload
	producer, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)
	defer producer.Close()

	record := &kgo.Record{
		Topic: "flashsale.order.events",
		Key:   []byte("Order"),
		Value: []byte(`{invalid-json}`),
	}
	err = producer.ProduceSync(ctx, record).FirstErr()
	require.NoError(t, err)

	// Verify DLQ
	dlqConsumer, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics("flashsale.inventory.dlq"),
		kgo.ConsumerGroup("test-dlq-checker"),
	)
	require.NoError(t, err)
	defer dlqConsumer.Close()

	fetches := dlqConsumer.PollFetches(ctx)
	errs := fetches.Errors()
	if len(errs) > 0 {
		t.Fatalf("Fetch errors: %v", errs)
	}

	records := fetches.Records()
	require.Len(t, records, 1)

	dlqRecord := records[0]
	assert.Equal(t, `{invalid-json}`, string(dlqRecord.Value))
}
