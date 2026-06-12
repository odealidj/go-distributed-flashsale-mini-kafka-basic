//go:build integration

package redis_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainersredis "github.com/testcontainers/testcontainers-go/modules/redis"

	outboundRedis "go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/outbound/redis"
)

func TestRedisPort_ReserveAndRefundStock(t *testing.T) {
	ctx := context.Background()

	// Spin up Redis Container
	redisContainer, err := testcontainersredis.RunContainer(ctx,
		testcontainers.WithImage("redis:7-alpine"),
	)
	require.NoError(t, err)
	defer func() {
		err := redisContainer.Terminate(ctx)
		assert.NoError(t, err)
	}()

	connStr, err := redisContainer.ConnectionString(ctx)
	require.NoError(t, err)

	opt, err := redis.ParseURL(connStr)
	require.NoError(t, err)

	client := redis.NewClient(opt)
	defer client.Close()

	// Ensure Redis is ready
	err = client.Ping(ctx).Err()
	require.NoError(t, err)

	redisPort := outboundRedis.NewRedisPort(client)

	t.Run("ReserveStock - Success", func(t *testing.T) {
		productID := "prod_success"
		eventID := "evt_success"
		quantity := 2

		// Set initial stock in Redis
		err := client.Set(ctx, fmt.Sprintf("stock:%s", productID), 10, 0).Err()
		require.NoError(t, err)

		// Reserve stock
		success, err := redisPort.ReserveStock(ctx, productID, eventID, quantity)
		assert.NoError(t, err)
		assert.True(t, success)

		// Verify stock decreased by quantity
		stockStr, err := client.Get(ctx, fmt.Sprintf("stock:%s", productID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, "8", stockStr)

		// Verify idempotency key is set with metadata "productID:quantity"
		meta, err := client.Get(ctx, fmt.Sprintf("reserve_idemp:%s", eventID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%s:%d", productID, quantity), meta)
	})

	t.Run("ReserveStock - Insufficient Stock", func(t *testing.T) {
		productID := "prod_insufficient"
		eventID := "evt_insufficient"

		// Set stock to 0
		err := client.Set(ctx, fmt.Sprintf("stock:%s", productID), 0, 0).Err()
		require.NoError(t, err)

		// Try to reserve
		success, err := redisPort.ReserveStock(ctx, productID, eventID, 1)
		assert.NoError(t, err)
		assert.False(t, success)

		// Verify stock remains 0
		stockStr, err := client.Get(ctx, fmt.Sprintf("stock:%s", productID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, "0", stockStr)
	})

	t.Run("ReserveStock - Idempotency Lock", func(t *testing.T) {
		productID := "prod_idemp"
		eventID := "evt_idemp"

		err := client.Set(ctx, fmt.Sprintf("stock:%s", productID), 5, 0).Err()
		require.NoError(t, err)

		// First call (success)
		success1, err := redisPort.ReserveStock(ctx, productID, eventID, 1)
		assert.NoError(t, err)
		assert.True(t, success1)

		// Second call with same eventID (rejection — idempotency)
		success2, err := redisPort.ReserveStock(ctx, productID, eventID, 1)
		assert.NoError(t, err)
		assert.False(t, success2)

		// Verify stock only decreased by 1 (not 2)
		stockStr, err := client.Get(ctx, fmt.Sprintf("stock:%s", productID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, "4", stockStr)
	})

	t.Run("RefundStock - Success", func(t *testing.T) {
		productID := "prod_refund"
		eventID := "evt_refund"

		err := client.Set(ctx, fmt.Sprintf("stock:%s", productID), 5, 0).Err()
		require.NoError(t, err)

		// First reserve 2 units
		success, err := redisPort.ReserveStock(ctx, productID, eventID, 2)
		require.NoError(t, err)
		require.True(t, success)

		// Refund stock
		refundSuccess, err := redisPort.RefundStock(ctx, productID, eventID, 2)
		assert.NoError(t, err)
		assert.True(t, refundSuccess)

		// Verify stock restored to 5
		stockStr, err := client.Get(ctx, fmt.Sprintf("stock:%s", productID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, "5", stockStr)

		// Verify idempotency key is deleted (can reserve again)
		idempExists, err := client.Exists(ctx, fmt.Sprintf("reserve_idemp:%s", eventID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, int64(0), idempExists)
	})

	t.Run("GetLeakedReservations - Detect Leaked Keys", func(t *testing.T) {
		productID := "prod_leak"
		eventID := "evt_leaked_123"
		quantity := 1

		err := client.Set(ctx, fmt.Sprintf("stock:%s", productID), 10, 0).Err()
		require.NoError(t, err)

		// Reserve stock (ini berhasil di Redis)
		success, err := redisPort.ReserveStock(ctx, productID, eventID, quantity)
		require.NoError(t, err)
		require.True(t, success)

		// Simulasikan idempotency key yang sudah "tua" (sudah melewati grace period)
		// dengan mengurangi TTL-nya agar tersisa sedikit (< maxTTL - gracePeriod)
		// maxTTL = 7200, gracePeriod = 5 menit (300 detik) → sisa TTL harus < 6900
		// Kita set TTL hanya 100 detik (jauh di bawah 6900 → terdeteksi sebagai leaked)
		err = client.Expire(ctx, fmt.Sprintf("reserve_idemp:%s", eventID), 100*time.Second).Err()
		require.NoError(t, err)

		leaked, err := redisPort.GetLeakedReservations(ctx, 300)
		assert.NoError(t, err)

		// Harus terdeteksi sebagai leaked
		meta, found := leaked[eventID]
		assert.True(t, found, "Reservasi bocor harus terdeteksi oleh GetLeakedReservations")
		assert.Equal(t, fmt.Sprintf("%s:%d", productID, quantity), meta)
	})

	t.Run("Concurrency - No Oversell Assert", func(t *testing.T) {
		productID := "prod_concurrency"
		initialStock := 50
		totalRequests := 150

		err := client.Set(ctx, fmt.Sprintf("stock:%s", productID), initialStock, 0).Err()
		require.NoError(t, err)

		var wg sync.WaitGroup
		var mu sync.Mutex
		successCount := 0
		failureCount := 0

		for i := 0; i < totalRequests; i++ {
			wg.Add(1)
			go func(reqNum int) {
				defer wg.Done()
				eventID := fmt.Sprintf("evt_concurrency_%d", reqNum)

				// Small random jitter to make it highly concurrent
				time.Sleep(time.Duration(reqNum%5) * time.Millisecond)

				success, err := redisPort.ReserveStock(ctx, productID, eventID, 1)
				if err != nil {
					t.Errorf("Error on ReserveStock concurrent call: %v", err)
					return
				}

				mu.Lock()
				if success {
					successCount++
				} else {
					failureCount++
				}
				mu.Unlock()
			}(i)
		}

		wg.Wait()

		// Assertions — tidak boleh ada oversell
		assert.Equal(t, initialStock, successCount, "Jumlah sukses harus tepat sama dengan stok awal")
		assert.Equal(t, totalRequests-initialStock, failureCount, "Jumlah gagal harus tepat sisanya")

		stockStr, err := client.Get(ctx, fmt.Sprintf("stock:%s", productID)).Result()
		assert.NoError(t, err)
		assert.Equal(t, "0", stockStr, "Stok akhir harus tepat 0 dan tidak boleh negatif (oversell)")
	})
}
