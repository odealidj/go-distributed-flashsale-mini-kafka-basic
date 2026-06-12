package model

import "time"

// Product merepresentasikan entitas bisnis produk di Flash Sale.
// Struktur ini bebas dari anotasi JSON/gRPC dan library eksternal.
type Product struct {
	ID             string
	Name           string
	OriginalPrice  int64
	FlashSalePrice int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	UpdatedBy      string
	Version        int32
}
