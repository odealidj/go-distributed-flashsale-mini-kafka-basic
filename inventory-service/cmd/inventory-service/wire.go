//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/inbound/grpc"
	redistore "go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/outbound/redis"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/outbound/postgres"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/usecase"
)

func initApp(db *sqlx.DB, rdb *redis.Client, logger log.Logger) (*grpc.InventoryServer, error) {
	panic(wire.Build(
		redistore.NewRedisPort,
		postgres.NewOutboxRepo,
		usecase.NewReserveStockUsecase,
		usecase.NewReleaseStockUsecase,
		grpc.NewInventoryServer,
	))
}
