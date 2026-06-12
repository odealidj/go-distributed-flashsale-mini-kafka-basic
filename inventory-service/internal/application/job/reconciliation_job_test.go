package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/job"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
)

func newTestJob(mockRedis *port.MockRedisPort, mockOutbox *port.MockOutboxPort) *job.ReconciliationJob {
	return job.NewReconciliationJob(mockRedis, mockOutbox, log.DefaultLogger).
		WithInterval(10 * time.Millisecond). // interval sangat cepat untuk test
		WithGracePeriod(300)
}

// ─────────────────────────────────────────────
// Skenario: tidak ada leaked reservation
// ─────────────────────────────────────────────

func TestReconciliationJob_NoLeaks(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)

	// GetLeakedReservations mengembalikan map kosong
	mockRedis.On("GetLeakedReservations", mock.Anything, 300).Return(map[string]string{}, nil)

	j := newTestJob(mockRedis, mockOutbox)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	j.Start(ctx) // blocking sampai ctx selesai

	// RefundStock tidak boleh pernah dipanggil
	mockOutbox.AssertNotCalled(t, "IsOutboxExist")
	mockRedis.AssertNotCalled(t, "RefundStock")
}

// ─────────────────────────────────────────────
// Skenario: stock leak terdeteksi dan direfund
// ─────────────────────────────────────────────

func TestReconciliationJob_LeakDetectedAndRefunded(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)

	leakedEventID := "evt-leaked-001"
	// meta format: "productID:quantity"
	leaked := map[string]string{leakedEventID: "prod_1:2"}

	mockRedis.On("GetLeakedReservations", mock.Anything, 300).Return(leaked, nil)
	// Event TIDAK ada di Postgres (konfirmasi: ini memang stock leak)
	mockOutbox.On("IsOutboxExist", mock.Anything, leakedEventID).Return(false, nil)
	// Refund berhasil
	mockRedis.On("RefundStock", mock.Anything, "prod_1", leakedEventID, 2).Return(true, nil)

	j := newTestJob(mockRedis, mockOutbox)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	j.Start(ctx)

	mockOutbox.AssertCalled(t, "IsOutboxExist", mock.Anything, leakedEventID)
	mockRedis.AssertCalled(t, "RefundStock", mock.Anything, "prod_1", leakedEventID, 2)
}

// ─────────────────────────────────────────────
// Skenario: false positive — key ada di Redis DAN di Postgres (bukan leak)
// ─────────────────────────────────────────────

func TestReconciliationJob_FalsePositive_SkipIfOutboxExists(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)

	eventID := "evt-success-001"
	leaked := map[string]string{eventID: "prod_2:1"}

	mockRedis.On("GetLeakedReservations", mock.Anything, 300).Return(leaked, nil)
	// Event ADA di Postgres → bukan leak, skip
	mockOutbox.On("IsOutboxExist", mock.Anything, eventID).Return(true, nil)

	j := newTestJob(mockRedis, mockOutbox)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	j.Start(ctx)

	// RefundStock TIDAK boleh dipanggil (bukan leak)
	mockRedis.AssertNotCalled(t, "RefundStock")
	mockOutbox.AssertExpectations(t)
}

// ─────────────────────────────────────────────
// Skenario: Redis scan error — job harus gracefully skip
// ─────────────────────────────────────────────

func TestReconciliationJob_RedisError_GracefullySkips(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)

	mockRedis.On("GetLeakedReservations", mock.Anything, 300).Return(nil, errors.New("redis timeout"))

	j := newTestJob(mockRedis, mockOutbox)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	// Tidak boleh panic, harus selesai dengan tenang
	assert.NotPanics(t, func() { j.Start(ctx) })
	mockOutbox.AssertNotCalled(t, "IsOutboxExist")
}

// ─────────────────────────────────────────────
// Skenario: multiple leaks — semua diproses
// ─────────────────────────────────────────────

func TestReconciliationJob_MultipleLeaks_AllRefunded(t *testing.T) {
	mockRedis := new(port.MockRedisPort)
	mockOutbox := new(port.MockOutboxPort)

	leaked := map[string]string{
		"evt-leak-A": "prod_1:1",
		"evt-leak-B": "prod_2:3",
	}

	mockRedis.On("GetLeakedReservations", mock.Anything, 300).Return(leaked, nil)
	mockOutbox.On("IsOutboxExist", mock.Anything, "evt-leak-A").Return(false, nil)
	mockOutbox.On("IsOutboxExist", mock.Anything, "evt-leak-B").Return(false, nil)
	mockRedis.On("RefundStock", mock.Anything, "prod_1", "evt-leak-A", 1).Return(true, nil)
	mockRedis.On("RefundStock", mock.Anything, "prod_2", "evt-leak-B", 3).Return(true, nil)

	j := newTestJob(mockRedis, mockOutbox)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	j.Start(ctx)

	mockRedis.AssertCalled(t, "RefundStock", mock.Anything, "prod_1", "evt-leak-A", 1)
	mockRedis.AssertCalled(t, "RefundStock", mock.Anything, "prod_2", "evt-leak-B", 3)
}
