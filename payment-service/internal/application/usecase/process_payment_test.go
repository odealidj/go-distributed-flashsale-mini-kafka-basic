package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go-flashsale-mini-kafka-basic/payment-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/payment-service/internal/application/usecase"
	"go-flashsale-mini-kafka-basic/payment-service/internal/domain/model"
)

func TestProcessPayment_Success(t *testing.T) {
	// Arrange
	mockRepo := new(port.MockPaymentRepository)
	uc := usecase.NewProcessPaymentUsecase(mockRepo)

	ctx := context.Background()
	orderID := "order-123"
	amount := int64(1000) // Akhiran bukan 4 (SUCCESS)

	// Kita ekspektasikan SavePaymentAndEmitEvent dipanggil 1 kali.
	// eventType harus "PaymentCompletedEvent"
	mockRepo.On("SavePaymentAndEmitEvent", ctx, mock.MatchedBy(func(p *model.Payment) bool {
		return p.OrderID == orderID && p.Amount == amount && p.Status == "SUCCESS"
	}), "PaymentCompletedEvent", mock.AnythingOfType("*model.PaymentCompletedEvent")).Return(nil)

	// Act
	success, err := uc.Execute(ctx, orderID, amount)

	// Assert
	assert.NoError(t, err)
	assert.True(t, success)
	mockRepo.AssertExpectations(t)
}

func TestProcessPayment_Failed(t *testing.T) {
	// Arrange
	mockRepo := new(port.MockPaymentRepository)
	uc := usecase.NewProcessPaymentUsecase(mockRepo)

	ctx := context.Background()
	orderID := "order-123"
	amount := int64(994) // Akhiran 4 (FAILED)

	// Kita ekspektasikan SavePaymentAndEmitEvent dipanggil 1 kali.
	// eventType harus "PaymentFailedEvent"
	mockRepo.On("SavePaymentAndEmitEvent", ctx, mock.MatchedBy(func(p *model.Payment) bool {
		return p.OrderID == orderID && p.Amount == amount && p.Status == "FAILED"
	}), "PaymentFailedEvent", mock.AnythingOfType("*model.PaymentFailedEvent")).Return(nil)

	// Act
	success, err := uc.Execute(ctx, orderID, amount)

	// Assert
	assert.NoError(t, err)
	assert.False(t, success) // Karena payment failed, uc me-return false
	mockRepo.AssertExpectations(t)
}
