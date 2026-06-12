package model

import "time"

type Inventory struct {
	ProductID string
	Stock     int64
	UpdatedAt time.Time
	Version   int32
}
