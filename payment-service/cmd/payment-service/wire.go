//go:build wireinject
// +build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/jmoiron/sqlx"

	"go-flashsale-mini-kafka-basic/payment-service/internal/adapter/inbound/grpc"
	"go-flashsale-mini-kafka-basic/payment-service/internal/adapter/outbound/postgres"
	"go-flashsale-mini-kafka-basic/payment-service/internal/application/usecase"
)

func InitializePaymentServer(db *sqlx.DB) *grpc.PaymentServer {
	panic(wire.Build(
		postgres.NewPaymentRepository,
		usecase.NewProcessPaymentUsecase,
		grpc.NewPaymentServer,
	))
}
