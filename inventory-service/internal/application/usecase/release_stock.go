package usecase

import (
	"context"
	"errors"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
)

// ReleaseStockUsecase menangani pelepasan stok secara manual via gRPC.
// Digunakan dalam skenario internal (misal: admin force-release, order timeout
// yang tidak melewati Kafka, atau integrasi dengan sistem legacy).
//
// Catatan: Pelepasan stok via Saga (Kafka OrderCancelledEvent) ditangani oleh
// Kafka Consumer secara terpisah. UseCase ini adalah jalur gRPC langsung (synchronous).
type ReleaseStockUsecase struct {
	redisPort port.RedisPort
}

func NewReleaseStockUsecase(redis port.RedisPort) *ReleaseStockUsecase {
	return &ReleaseStockUsecase{redisPort: redis}
}

// Execute mengembalikan stok ke Redis secara atomik via Lua Script.
// eventID digunakan sebagai idempotency key untuk menghapus reservasi sebelumnya.
func (uc *ReleaseStockUsecase) Execute(ctx context.Context, productID string, eventID string, quantity int) error {
	if productID == "" || eventID == "" {
		return errors.New("productID dan eventID tidak boleh kosong")
	}
	if quantity <= 0 {
		return errors.New("quantity harus lebih dari 0")
	}

	success, err := uc.redisPort.RefundStock(ctx, productID, eventID, quantity)
	if err != nil {
		return err
	}
	if !success {
		// Idempotency key tidak ditemukan — stok mungkin sudah pernah di-release
		// atau eventID ini belum pernah mereservasi stok. Bukan error fatal.
		return errors.New("release gagal: idempotency key tidak ditemukan, stok mungkin sudah dirilis sebelumnya")
	}

	return nil
}
