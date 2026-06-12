package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/jmoiron/sqlx"

	"go-flashsale-mini-kafka-basic/order-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/order-service/internal/domain/model"
)

// TimeoutWorker adalah background worker yang mencari order PENDING
// yang sudah melewati batas waktu (timeout) dan membatalkannya.
type TimeoutWorker struct {
	db     *sqlx.DB
	repo   port.OrderRepository
	logger *log.Helper
}

func NewTimeoutWorker(db *sqlx.DB, repo port.OrderRepository, logger log.Logger) *TimeoutWorker {
	return &TimeoutWorker{
		db:     db,
		repo:   repo,
		logger: log.NewHelper(logger),
	}
}

// Start menjalankan background loop.
func (w *TimeoutWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Cek setiap 30 detik
	defer ticker.Stop()

	w.logger.Info("Starting Order Timeout Worker")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Stopping Order Timeout Worker")
			return
		case <-ticker.C:
			w.processExpiredOrders(ctx)
		}
	}
}

func (w *TimeoutWorker) processExpiredOrders(ctx context.Context) {
	tx, err := w.db.BeginTxx(ctx, nil)
	if err != nil {
		w.logger.Errorf("Gagal memulai transaksi TimeoutWorker: %v", err)
		return
	}
	defer tx.Rollback()

	var orders []model.Order
	// Cari order PENDING yang usianya > 15 menit
	// Menggunakan FOR UPDATE SKIP LOCKED agar jika ada replika worker lain,
	// mereka tidak saling blok dan memproses baris yang berbeda.
	err = tx.SelectContext(ctx, &orders, `
		SELECT id, user_id, product_id, quantity, total_amount, status, created_at, updated_at
		FROM orders
		WHERE status = 'PENDING' AND created_at < NOW() - INTERVAL '15 minutes'
		LIMIT 100
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		w.logger.Errorf("Gagal query expired orders: %v", err)
		return
	}

	if len(orders) == 0 {
		return // Tidak ada order expired
	}

	for _, order := range orders {
		w.logger.Infof("Membatalkan order %s karena timeout", order.ID)

		cancelEvent := &model.OrderCancelledEvent{
			EventID:   uuid.New().String(), // Generate event ID baru
			OrderID:   order.ID,
			ProductID: order.ProductID,
			Quantity:  order.Quantity,
			Reason:    "Order expired after 15 minutes",
		}

		// Karena repo CancelOrderAndEmitEvent juga menggunakan transaksi sendiri,
		// kita tidak bisa memakainya langsung jika kita sedang dalam lock di atas, 
		// KECUALI kita pindahkan logikanya atau commit dulu.
		// Oleh karena itu, lebih baik kita panggil repo untuk satu per satu, tapi itu akan memutus lock.
		// Solusi terbaik: Lakukan update dan insert outbox di dalam transaksi worker ini secara batch,
		// ATAU panggil method repo dari luar transaksi.

		// Untuk menghindari deadlock, kita lepaskan lock dengan commit transaksi awal,
		// baru memprosesnya satu-satu memanggil CancelOrderAndEmitEvent.
		// Namun ini bisa membuat race condition jika order dibayar di saat yang sama.
		
		// Mari kita implementasi langsung di dalam transaksi ini untuk keandalan penuh.
		_, err = tx.ExecContext(ctx, "UPDATE orders SET status = 'CANCELLED', updated_at = CURRENT_TIMESTAMP WHERE id = $1", order.ID)
		if err != nil {
			w.logger.Errorf("Gagal update order %s ke CANCELLED: %v", order.ID, err)
			continue
		}

		importJson := true
		_ = importJson
		
		// Insert outbox
		payload := fmt.Sprintf(`{"event_id":"%s","order_id":"%s","product_id":"%s","quantity":%d,"reason":"%s"}`,
			cancelEvent.EventID, cancelEvent.OrderID, cancelEvent.ProductID, cancelEvent.Quantity, cancelEvent.Reason)
		
		_, err = tx.ExecContext(ctx, `
			INSERT INTO outbox_messages (aggregate_id, aggregate_type, event_type, payload, status)
			VALUES ($1, $2, $3, $4, 'PENDING')
		`, order.ID, "order", "OrderCancelledEvent", payload)
		if err != nil {
			w.logger.Errorf("Gagal insert outbox event untuk order %s: %v", order.ID, err)
			// transaksi ini untuk batch, error 1 order akan lanjut ke order lain (atau kita bisa rollback)
			// karena ini perulangan, idealnya db operation ada dalam 1 tx per order atau tx rollback all.
		}
	}
	
	if err := tx.Commit(); err != nil {
		w.logger.Errorf("Gagal commit transaksi timeout orders: %v", err)
	}
}
