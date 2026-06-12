package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/usecase"
)

// ─────────────────────────────────────────────
// Happy Path Tests
// ─────────────────────────────────────────────

func TestReserveStockUsecase_Execute_Success(t *testing.T) {
	// Arrange
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)
	uc := usecase.NewReserveStockUsecase(mockRedis, mockOutbox)

	ctx := context.Background()
	productID := "prod_1"
	userID := "user-001"
	eventID := "evt-abc-123"
	quantity := 2

	mockRedis.On("ReserveStock", ctx, productID, eventID, quantity).Return(true, nil)
	mockOutbox.On("InsertOutbox", ctx, eventID, "Inventory", "StockReservedEvent", mock.AnythingOfType("[]uint8")).Return(nil)

	// Act
	err := uc.Execute(ctx, productID, userID, eventID, quantity)

	// Assert
	assert.NoError(t, err)
	mockRedis.AssertExpectations(t)
	mockOutbox.AssertExpectations(t)
}

// ─────────────────────────────────────────────
// Redis Failure Tests
// ─────────────────────────────────────────────

func TestReserveStockUsecase_Execute_StockExhausted(t *testing.T) {
	// Arrange: Redis mengembalikan false (stok habis)
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)
	uc := usecase.NewReserveStockUsecase(mockRedis, mockOutbox)

	ctx := context.Background()
	mockRedis.On("ReserveStock", ctx, "prod_1", "evt-001", 1).Return(false, nil)

	// Act
	err := uc.Execute(ctx, "prod_1", "user-1", "evt-001", 1)

	// Assert: harus error domain "stok habis"
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stok habis")
	// Outbox TIDAK boleh dipanggil sama sekali
	mockOutbox.AssertNotCalled(t, "InsertOutbox")
}

func TestReserveStockUsecase_Execute_IdempotencyDuplicate(t *testing.T) {
	// Arrange: Redis mengembalikan false karena eventID sudah diproses (idempotency)
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)
	uc := usecase.NewReserveStockUsecase(mockRedis, mockOutbox)

	ctx := context.Background()
	// Lua Script mengembalikan false saat idemp key sudah ada (duplicate event)
	mockRedis.On("ReserveStock", ctx, "prod_1", "evt-duplicate", 1).Return(false, nil)

	// Act
	err := uc.Execute(ctx, "prod_1", "user-1", "evt-duplicate", 1)

	// Assert
	assert.Error(t, err)
	mockOutbox.AssertNotCalled(t, "InsertOutbox")
	mockRedis.AssertExpectations(t)
}

func TestReserveStockUsecase_Execute_RedisConnectionError(t *testing.T) {
	// Arrange: Redis error koneksi
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)
	uc := usecase.NewReserveStockUsecase(mockRedis, mockOutbox)

	ctx := context.Background()
	redisErr := errors.New("redis: connection refused")
	mockRedis.On("ReserveStock", ctx, "prod_1", "evt-002", 1).Return(false, redisErr)

	// Act
	err := uc.Execute(ctx, "prod_1", "user-1", "evt-002", 1)

	// Assert: error koneksi diteruskan ke caller
	assert.Error(t, err)
	assert.Equal(t, redisErr, err)
	mockOutbox.AssertNotCalled(t, "InsertOutbox")
}

// ─────────────────────────────────────────────
// Outbox / Postgres Failure Tests
// ─────────────────────────────────────────────

func TestReserveStockUsecase_Execute_OutboxInsertFails(t *testing.T) {
	// Arrange: Redis berhasil, tapi Postgres gagal → kondisi "stock leak" potensial
	// ReconciliationJob yang akan menangani ini.
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)
	uc := usecase.NewReserveStockUsecase(mockRedis, mockOutbox)

	ctx := context.Background()
	mockRedis.On("ReserveStock", ctx, "prod_1", "evt-003", 1).Return(true, nil)
	mockOutbox.On("InsertOutbox", ctx, "evt-003", "Inventory", "StockReservedEvent",
		mock.AnythingOfType("[]uint8")).Return(errors.New("connection to postgres lost"))

	// Act
	err := uc.Execute(ctx, "prod_1", "user-1", "evt-003", 1)

	// Assert: error outbox dikembalikan ke caller
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gagal menyimpan outbox")
	mockRedis.AssertExpectations(t)
	mockOutbox.AssertExpectations(t)
}

// ─────────────────────────────────────────────
// Payload Correctness Test
// ─────────────────────────────────────────────

func TestReserveStockUsecase_Execute_PayloadDoesNotContainPrice(t *testing.T) {
	// Verify: price TIDAK ada di payload Kafka (Bounded Context — price = urusan Order Service)
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)
	uc := usecase.NewReserveStockUsecase(mockRedis, mockOutbox)

	ctx := context.Background()
	mockRedis.On("ReserveStock", ctx, "prod_1", "evt-payload", 3).Return(true, nil)

	var capturedPayload []byte
	mockOutbox.On("InsertOutbox", ctx, "evt-payload", "Inventory", "StockReservedEvent",
		mock.MatchedBy(func(p []byte) bool {
			capturedPayload = p
			return true
		})).Return(nil)

	// Act
	err := uc.Execute(ctx, "prod_1", "user-1", "evt-payload", 3)
	assert.NoError(t, err)

	// Assert: payload berisi field yang benar dan TIDAK ada "price"
	payloadStr := string(capturedPayload)
	assert.Contains(t, payloadStr, `"event_id"`)
	assert.Contains(t, payloadStr, `"product_id"`)
	assert.Contains(t, payloadStr, `"user_id"`)
	assert.Contains(t, payloadStr, `"quantity"`)
	assert.Contains(t, payloadStr, `"status"`)
	assert.NotContains(t, payloadStr, `"price"`, "price TIDAK boleh ada di payload — ini ranah Order Service")
}
