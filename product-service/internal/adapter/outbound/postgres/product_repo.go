package postgres

import (
	"context"

	"github.com/jmoiron/sqlx"
	"go-flashsale-mini-kafka-basic/product-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/product-service/internal/domain/model"
)

type productRepo struct {
	db *sqlx.DB
}

func NewProductRepo(db *sqlx.DB) port.ProductRepository {
	return &productRepo{db: db}
}

func (r *productRepo) ListFlashSaleProducts(ctx context.Context, page int32, perPage int32) ([]*model.Product, int32, error) {
	// Dummy implementation for scaffolding.
	// Di production, ini akan mengeksekusi SELECT * FROM products LIMIT perPage OFFSET ...
	// Dan untuk Flash Sale, ini harusnya dibungkus oleh Redis Cache.

	dummyProducts := []*model.Product{
		{
			ID:             "prod_1",
			Name:           "Sepatu Lari X",
			OriginalPrice:  500000,
			FlashSalePrice: 150000,
		},
		{
			ID:             "prod_2",
			Name:           "Tas Ransel Y",
			OriginalPrice:  300000,
			FlashSalePrice: 99000,
		},
	}

	return dummyProducts, int32(len(dummyProducts)), nil
}
