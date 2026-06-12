package usecase

import (
	"context"
	"github.com/google/uuid"

	"go-flashsale-mini-kafka-basic/payment-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/payment-service/internal/domain/model"
)

type ProcessPaymentUsecase struct {
	repo port.PaymentRepository
}

func NewProcessPaymentUsecase(repo port.PaymentRepository) *ProcessPaymentUsecase {
	return &ProcessPaymentUsecase{repo: repo}
}

func (uc *ProcessPaymentUsecase) Execute(ctx context.Context, orderID string, amount int64) (bool, error) {
	payment := &model.Payment{
		ID:      uuid.New().String(),
		OrderID: orderID,
		Amount:  amount,
	}

	var event interface{}
	var eventType string

	// Simulasi payment gagal jika amount berakhiran angka 4
	if amount%10 == 4 {
		payment.Status = "FAILED"
		eventType = "PaymentFailedEvent"
		event = &model.PaymentFailedEvent{
			EventID: uuid.New().String(),
			OrderID: payment.OrderID,
			Amount:  payment.Amount,
			Reason:  "Payment declined by bank",
		}
	} else {
		payment.Status = "SUCCESS"
		eventType = "PaymentCompletedEvent"
		event = &model.PaymentCompletedEvent{
			EventID: uuid.New().String(),
			OrderID: payment.OrderID,
			Amount:  payment.Amount,
		}
	}

	// Simpan ke DB dan catat ke Outbox secara atomik
	err := uc.repo.SavePaymentAndEmitEvent(ctx, payment, eventType, event)
	if err != nil {
		return false, err
	}

	return payment.Status == "SUCCESS", nil
}
