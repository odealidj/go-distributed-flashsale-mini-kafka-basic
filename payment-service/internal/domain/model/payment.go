package model

import "time"

type Payment struct {
	ID        string
	OrderID   string
	Amount    int64
	Status    string // SUCCESS, FAILED
	CreatedAt time.Time
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
