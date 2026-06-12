package usecase

import (
	"context"
	"go-flashsale-mini-kafka-basic/product-service/internal/application/port"
	"go-flashsale-mini-kafka-basic/product-service/internal/domain/model"
)

// ListFlashSaleProductsUsecase adalah orkestrator logika bisnis untuk mengambil produk.
type ListFlashSaleProductsUsecase struct {
	repo port.ProductRepository
}

// NewListFlashSaleProductsUsecase adalah constructor untuk dependency injection.
func NewListFlashSaleProductsUsecase(repo port.ProductRepository) *ListFlashSaleProductsUsecase {
	return &ListFlashSaleProductsUsecase{
		repo: repo,
	}
}

// Execute memanggil layer bawah (adapter) melalui interface.
func (uc *ListFlashSaleProductsUsecase) Execute(ctx context.Context, page, perPage int32) ([]*model.Product, int32, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return uc.repo.ListFlashSaleProducts(ctx, page, perPage)
}
