package port

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockRedisPort adalah mock testify untuk RedisPort.
type MockRedisPort struct {
	mock.Mock
}

func (m *MockRedisPort) ReserveStock(ctx context.Context, productID string, eventID string, quantity int) (bool, error) {
	args := m.Called(ctx, productID, eventID, quantity)
	return args.Bool(0), args.Error(1)
}

func (m *MockRedisPort) RefundStock(ctx context.Context, productID string, eventID string, quantity int) (bool, error) {
	args := m.Called(ctx, productID, eventID, quantity)
	return args.Bool(0), args.Error(1)
}

func (m *MockRedisPort) GetLeakedReservations(ctx context.Context, gracePeriodSecs int) (map[string]string, error) {
	args := m.Called(ctx, gracePeriodSecs)
	if args.Get(0) != nil {
		return args.Get(0).(map[string]string), args.Error(1)
	}
	return nil, args.Error(1)
}

// MockOutboxPort adalah mock testify untuk OutboxPort.
type MockOutboxPort struct {
	mock.Mock
}

func (m *MockOutboxPort) InsertOutbox(ctx context.Context, aggregateID string, aggregateType string, eventType string, payload []byte) error {
	args := m.Called(ctx, aggregateID, aggregateType, eventType, payload)
	return args.Error(0)
}

func (m *MockOutboxPort) IsOutboxExist(ctx context.Context, aggregateID string) (bool, error) {
	args := m.Called(ctx, aggregateID)
	return args.Bool(0), args.Error(1)
}
