// Package database menyediakan helper untuk inisialisasi koneksi database
// dengan pengaturan connection pool yang aman untuk beban Flash Sale.
package database

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// Config adalah konfigurasi connection pool PostgreSQL.
type Config struct {
	DSN string
	// MaxOpenConns adalah jumlah maksimum koneksi aktif ke database.
	// Hindari nilai yang terlalu besar — PostgreSQL punya batas per-instance.
	// Default: 25 (aman untuk instance kecil, sesuaikan dengan max_connections di postgres.conf).
	MaxOpenConns int
	// MaxIdleConns adalah jumlah koneksi idle yang dipertahankan di pool.
	// Harus <= MaxOpenConns.
	MaxIdleConns int
	// ConnMaxLifetime adalah usia maksimum sebuah koneksi sebelum diganti baru.
	// Mencegah koneksi "basi" yang mungkin sudah ditutup sisi server.
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime adalah waktu maksimum koneksi idle sebelum ditutup.
	ConnMaxIdleTime time.Duration
}

// DefaultConfig mengembalikan konfigurasi connection pool default
// yang aman untuk beban Flash Sale pada instance kecil-menengah.
func DefaultConfig(dsn string) Config {
	return Config{
		DSN:             dsn,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}
}

// Connect membuat koneksi PostgreSQL dengan connection pool yang dikonfigurasi.
// Selalu gunakan fungsi ini untuk koneksi DB — jangan gunakan sqlx.Connect langsung.
//
// Contoh:
//
//	db, err := database.Connect(database.DefaultConfig(os.Getenv("DATABASE_URL")))
func Connect(cfg Config) (*sqlx.DB, error) {
	db, err := sqlx.Connect("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("gagal koneksi ke database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return db, nil
}
