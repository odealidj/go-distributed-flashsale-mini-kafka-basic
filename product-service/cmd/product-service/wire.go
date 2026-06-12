//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/jmoiron/sqlx"

	"go-flashsale-mini-kafka-basic/product-service/internal/adapter/inbound/grpc"
	"go-flashsale-mini-kafka-basic/product-service/internal/adapter/outbound/postgres"
	"go-flashsale-mini-kafka-basic/product-service/internal/application/usecase"
)

func initApp(db *sqlx.DB, logger log.Logger) (*grpc.ProductServer, error) {
	panic(wire.Build(
		postgres.NewProductRepo,
		usecase.NewListFlashSaleProductsUsecase,
		grpc.NewProductServer,
	))
}
