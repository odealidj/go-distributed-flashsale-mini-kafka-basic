package port

import (
	"context"

	"github.com/stretchr/testify/mock"
	"go-flashsale-mini-kafka-basic/payment-service/internal/domain/model"
)

// MockPaymentRepository adalah mock untuk PaymentRepository menggunakan testify/mock.
type MockPaymentRepository struct {
	mock.Mock
}

func (m *MockPaymentRepository) SavePaymentAndEmitEvent(ctx context.Context, payment *model.Payment, eventType string, event interface{}) error {
	args := m.Called(ctx, payment, eventType, event)
	return args.Error(0)
}
