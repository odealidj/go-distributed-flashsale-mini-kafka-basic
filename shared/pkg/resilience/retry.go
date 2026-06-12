package resilience

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// ErrMaxRetriesExceeded dikembalikan ketika semua percobaan retry telah habis.
var ErrMaxRetriesExceeded = errors.New("max retries exceeded")

// RetryConfig mengatur perilaku mekanisme retry.
type RetryConfig struct {
	// MaxAttempts adalah jumlah total percobaan (termasuk percobaan pertama).
	MaxAttempts int
	// InitialInterval adalah jeda sebelum percobaan ke-2.
	InitialInterval time.Duration
	// MaxInterval adalah batas atas jeda antar-retry.
	MaxInterval time.Duration
	// Multiplier adalah faktor pengali jeda (untuk exponential backoff).
	Multiplier float64
	// Jitter menambahkan variasi acak untuk menghindari thundering herd antar retry.
	Jitter bool
	// IsRetryable adalah fungsi untuk menentukan apakah error layak di-retry.
	// Jika nil, semua error akan di-retry.
	IsRetryable func(err error) bool
}

// DefaultRetryConfig mengembalikan konfigurasi retry default yang aman
// untuk pemanggilan gRPC internal dengan exponential backoff.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Multiplier:      2.0,
		Jitter:          true,
		IsRetryable:     nil, // retry semua error
	}
}

// DoWithRetry menjalankan fungsi fn dengan retry menggunakan exponential backoff.
// Cocok untuk memanggil service downstream yang bisa mengalami transient failure.
//
// Contoh penggunaan:
//
//	err := resilience.DoWithRetry(ctx, resilience.DefaultRetryConfig(), func(attempt int) error {
//	    _, err := client.ReserveStock(ctx, req)
//	    return err
//	})
func DoWithRetry(ctx context.Context, cfg RetryConfig, fn func(attempt int) error) error {
	var lastErr error
	interval := cfg.InitialInterval

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		// Cek apakah context sudah dibatalkan
		if ctx.Err() != nil {
			return ctx.Err()
		}

		lastErr = fn(attempt)
		if lastErr == nil {
			return nil // Sukses
		}

		// Cek apakah error ini layak di-retry
		if cfg.IsRetryable != nil && !cfg.IsRetryable(lastErr) {
			return lastErr // Error tidak layak retry, langsung return
		}

		// Jika ini percobaan terakhir, jangan tunggu
		if attempt == cfg.MaxAttempts {
			break
		}

		// Hitung jeda dengan exponential backoff
		sleepDuration := time.Duration(float64(interval) * math.Pow(cfg.Multiplier, float64(attempt-1)))
		if sleepDuration > cfg.MaxInterval {
			sleepDuration = cfg.MaxInterval
		}

		// Tambah jitter untuk menghindari "retry storm"
		if cfg.Jitter {
			jitterRange := float64(sleepDuration) * 0.3 // ±30% jitter
			sleepDuration += time.Duration(rand.Float64()*jitterRange*2 - jitterRange)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
			// Lanjut ke percobaan berikutnya
		}
	}

	return errors.Join(ErrMaxRetriesExceeded, lastErr)
}
