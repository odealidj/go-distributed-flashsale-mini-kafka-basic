package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/usecase"
)

func TestReleaseStockUsecase_Execute_Success(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	uc := usecase.NewReleaseStockUsecase(mockRedis)

	ctx := context.Background()
	mockRedis.On("RefundStock", ctx, "prod_1", "evt-release-001", 2).Return(true, nil)

	err := uc.Execute(ctx, "prod_1", "evt-release-001", 2)

	assert.NoError(t, err)
	mockRedis.AssertExpectations(t)
}

func TestReleaseStockUsecase_Execute_KeyNotFound(t *testing.T) {
	// Skenario: idempotency key tidak ada di Redis (stok sudah pernah dirilis)
	mockRedis := new(port.MockRedisPort)
	uc := usecase.NewReleaseStockUsecase(mockRedis)

	ctx := context.Background()
	mockRedis.On("RefundStock", ctx, "prod_1", "evt-already-released", 1).Return(false, nil)

	err := uc.Execute(ctx, "prod_1", "evt-already-released", 1)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency key tidak ditemukan")
}

func TestReleaseStockUsecase_Execute_RedisError(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	uc := usecase.NewReleaseStockUsecase(mockRedis)

	ctx := context.Background()
	mockRedis.On("RefundStock", ctx, "prod_1", "evt-err", 1).Return(false, errors.New("redis unavailable"))

	err := uc.Execute(ctx, "prod_1", "evt-err", 1)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis unavailable")
}

func TestReleaseStockUsecase_Execute_ValidationErrors(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	uc := usecase.NewReleaseStockUsecase(mockRedis)
	ctx := context.Background()

	t.Run("empty productID", func(t *testing.T) {
		err := uc.Execute(ctx, "", "evt-1", 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak boleh kosong")
	})

	t.Run("empty eventID", func(t *testing.T) {
		err := uc.Execute(ctx, "prod_1", "", 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak boleh kosong")
	})

	t.Run("zero quantity", func(t *testing.T) {
		err := uc.Execute(ctx, "prod_1", "evt-1", 0)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "quantity harus lebih dari 0")
	})

	t.Run("negative quantity", func(t *testing.T) {
		err := uc.Execute(ctx, "prod_1", "evt-1", -5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "quantity harus lebih dari 0")
	})

	// RefundStock tidak boleh dipanggil sama sekali untuk input tidak valid
	mockRedis.AssertNotCalled(t, "RefundStock")
}
