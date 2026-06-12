package model

import "time"

type Order struct {
	ID          string    `db:"id"`
	UserID      string    `db:"user_id"`
	ProductID   string    `db:"product_id"`
	Quantity    int       `db:"quantity"`
	TotalAmount int64     `db:"total_amount"`
	Status      string    `db:"status"` // PENDING, PAID, CANCELLED
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

type StockReservedEvent struct {
	EventID        string `json:"event_id"`
	IdempotencyKey string `json:"idempotency_key"` // Digunakan sebagai order_id
	UserID         string `json:"user_id"`
	ProductID      string `json:"product_id"`
	Quantity       int    `json:"quantity"`
	Price          int64  `json:"price"` // Untuk simplicity, simpan price agar bisa hitung total
}

type PaymentCompletedEvent struct {
	EventID string `json:"event_id"`
	OrderID string `json:"order_id"`
	Amount  int64  `json:"amount"`
}

type PaymentFailedEvent struct {
	EventID string `json:"event_id"`
	OrderID string `json:"order_id"`
	Amount  int64  `json:"amount"`
	Reason  string `json:"reason"`
}

type OrderCancelledEvent struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
	Reason    string `json:"reason"`
}
