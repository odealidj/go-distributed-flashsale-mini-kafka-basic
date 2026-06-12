// Package job berisi background jobs yang berjalan secara periodik
// sebagai bagian dari lifecycle Inventory Service.
package job

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
)

const (
	// defaultGracePeriod adalah waktu minimum (dalam detik) sebelum sebuah reservasi
	// dianggap "bocor" dan memerlukan reconciliation.
	// 5 menit memberikan cukup waktu bagi Outbox Processor normal untuk bekerja.
	defaultGracePeriod = 5 * 60 // 300 detik = 5 menit

	// defaultInterval adalah seberapa sering ReconciliationJob berjalan.
	defaultInterval = 1 * time.Minute
)

// ReconciliationJob adalah background job yang mendeteksi dan memperbaiki "stock leak".
//
// Stock Leak terjadi ketika:
//   1. ReserveStock berhasil memotong stok di Redis (atomik).
//   2. Namun INSERT ke Postgres Outbox gagal (crash, network error, dsb).
//   3. Akibatnya: stok terpotong permanen di Redis, tapi event "StockReserved"
//      tidak pernah terkirim ke Kafka → Order tidak pernah dibuat.
//
// ReconciliationJob mendeteksi kondisi ini dengan membandingkan:
//   - Idempotency key yang masih ada di Redis (reserve_idemp:{eventID})
//   - Record yang ADA di Postgres outbox_messages
//
// Jika sebuah eventID ada di Redis tapi TIDAK ada di Postgres setelah grace period,
// stok dikembalikan (RefundStock) secara atomik.
//
// Ini adalah implementasi "Compensating Transaction" — pola standar di real production
// untuk menjaga konsistensi di sistem terdistribusi tanpa Distributed Transaction.
type ReconciliationJob struct {
	redisPort  port.RedisPort
	outboxPort port.OutboxPort
	logger     *log.Helper
	interval   time.Duration
	gracePeriod int
}

// NewReconciliationJob membuat instance baru ReconciliationJob.
func NewReconciliationJob(redis port.RedisPort, outbox port.OutboxPort, logger log.Logger) *ReconciliationJob {
	return &ReconciliationJob{
		redisPort:   redis,
		outboxPort:  outbox,
		logger:      log.NewHelper(log.With(logger, "component", "reconciliation-job")),
		interval:    defaultInterval,
		gracePeriod: defaultGracePeriod,
	}
}

// WithInterval memungkinkan konfigurasi interval custom (untuk testing atau fine-tuning).
func (j *ReconciliationJob) WithInterval(d time.Duration) *ReconciliationJob {
	j.interval = d
	return j
}

// WithGracePeriod memungkinkan konfigurasi grace period custom dalam detik.
func (j *ReconciliationJob) WithGracePeriod(secs int) *ReconciliationJob {
	j.gracePeriod = secs
	return j
}

// Start menjalankan ReconciliationJob secara blocking sampai context dibatalkan.
// Panggil ini dengan goroutine: go job.Start(ctx)
func (j *ReconciliationJob) Start(ctx context.Context) {
	j.logger.Infof("ReconciliationJob dimulai. Interval: %v, Grace Period: %ds",
		j.interval, j.gracePeriod)

	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	// Jalankan sekali saat startup untuk menangkap leak dari restart sebelumnya
	j.run(ctx)

	for {
		select {
		case <-ctx.Done():
			j.logger.Info("ReconciliationJob dihentikan")
			return
		case <-ticker.C:
			j.run(ctx)
		}
	}
}

// run adalah satu siklus eksekusi reconciliation.
func (j *ReconciliationJob) run(ctx context.Context) {
	j.logger.Debug("ReconciliationJob: memulai siklus pemeriksaan...")

	// 1. Ambil semua reservasi yang sudah melewati grace period dari Redis
	leaked, err := j.redisPort.GetLeakedReservations(ctx, j.gracePeriod)
	if err != nil {
		j.logger.Errorf("ReconciliationJob: gagal membaca leaked reservations dari Redis: %v", err)
		return
	}

	if len(leaked) == 0 {
		j.logger.Debug("ReconciliationJob: tidak ada leaked reservation yang terdeteksi")
		return
	}

	j.logger.Warnf("ReconciliationJob: mendeteksi %d kandidat leaked reservation, memulai verifikasi...",
		len(leaked))

	refunded := 0
	for eventID, meta := range leaked {
		if err := j.reconcile(ctx, eventID, meta); err != nil {
			j.logger.Errorf("ReconciliationJob: gagal reconcile eventID=%s: %v", eventID, err)
		} else {
			refunded++
		}
	}

	if refunded > 0 {
		j.logger.Warnf("ReconciliationJob: berhasil merefund %d leaked reservation(s)", refunded)
	}
}

// reconcile memverifikasi satu reservasi dan merefund jika benar-benar bocor.
func (j *ReconciliationJob) reconcile(ctx context.Context, eventID string, meta string) error {
	// 2. Cek apakah event ini sudah ada di Postgres Outbox
	exists, err := j.outboxPort.IsOutboxExist(ctx, eventID)
	if err != nil {
		return fmt.Errorf("gagal query outbox untuk eventID=%s: %w", eventID, err)
	}

	if exists {
		// Event ADA di Postgres → ini bukan leak, abaikan
		j.logger.Debugf("ReconciliationJob: eventID=%s sudah ada di outbox, skip", eventID)
		return nil
	}

	// 3. Event TIDAK ada di Postgres → ini adalah stock leak, refund stok
	productID, quantity, err := parseMeta(meta)
	if err != nil {
		return fmt.Errorf("gagal parse meta '%s' untuk eventID=%s: %w", meta, eventID, err)
	}

	j.logger.Warnf("ReconciliationJob: STOCK LEAK terdeteksi! eventID=%s, productID=%s, quantity=%d. Mengembalikan stok...",
		eventID, productID, quantity)

	success, err := j.redisPort.RefundStock(ctx, productID, eventID, quantity)
	if err != nil {
		return fmt.Errorf("gagal refund stok untuk eventID=%s: %w", eventID, err)
	}

	if !success {
		// Key sudah hilang (expired) — stok tidak perlu dikembalikan
		j.logger.Warnf("ReconciliationJob: idempotency key untuk eventID=%s sudah expire saat refund, mungkin sudah di-GC oleh Redis", eventID)
		return nil
	}

	j.logger.Infof("ReconciliationJob: stok berhasil dikembalikan untuk eventID=%s, productID=%s, quantity=%d",
		eventID, productID, quantity)
	return nil
}

// parseMeta mem-parse string "productID:quantity" menjadi komponen-komponennya.
func parseMeta(meta string) (productID string, quantity int, err error) {
	// Format: "{productID}:{quantity}" — productID bisa mengandung karakter apapun kecuali ":"
	// Gunakan LastIndex agar product_id yang berisi "-" tidak terpotong
	idx := strings.LastIndex(meta, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("format meta tidak valid: %q", meta)
	}
	productID = meta[:idx]
	quantityStr := meta[idx+1:]

	q, err := strconv.Atoi(quantityStr)
	if err != nil {
		return "", 0, fmt.Errorf("quantity bukan angka: %q", quantityStr)
	}
	return productID, q, nil
}
