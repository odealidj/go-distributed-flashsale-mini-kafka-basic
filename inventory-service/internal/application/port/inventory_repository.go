package port

import (
	"context"
)

// RedisPort menangani operasi atomik di Redis (Atomic Counter & Idempotency).
type RedisPort interface {
	// ReserveStock menjalankan Lua Script: cek stok > 0, kurangi sejumlah quantity,
	// simpan idempotency key berisi metadata "productID:quantity" (untuk reconciliation).
	ReserveStock(ctx context.Context, productID string, eventID string, quantity int) (bool, error)

	// RefundStock menjalankan Lua Script untuk mengembalikan stok dan menghapus idempotency key.
	RefundStock(ctx context.Context, productID string, eventID string, quantity int) (bool, error)

	// GetLeakedReservations mencari reservasi yang sudah melewati grace period (dalam detik)
	// namun idempotency key-nya masih ada di Redis.
	// Mengembalikan map eventID -> "productID:quantity" untuk diproses ReconciliationJob.
	GetLeakedReservations(ctx context.Context, gracePeriodSecs int) (map[string]string, error)
}

// OutboxPort menangani penyimpanan event ke database Postgres di dalam transaksi.
type OutboxPort interface {
	// InsertOutbox menyimpan payload (misal: JSON StockReservedEvent) ke tabel outbox_messages.
	InsertOutbox(ctx context.Context, aggregateID string, aggregateType string, eventType string, payload []byte) error

	// IsOutboxExist mengecek apakah record outbox dengan aggregate_id tertentu sudah ada.
	// Digunakan oleh ReconciliationJob untuk memverifikasi apakah event benar-benar "bocor"
	// (ada di Redis tapi tidak pernah masuk ke Postgres).
	IsOutboxExist(ctx context.Context, aggregateID string) (bool, error)
}
