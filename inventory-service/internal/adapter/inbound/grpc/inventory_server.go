package grpc

import (
	"context"

	pb "go-flashsale-mini-kafka-basic/proto/inventory/v1"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/usecase"
)

type InventoryServer struct {
	pb.UnimplementedInventoryServiceServer
	reserveUsecase *usecase.ReserveStockUsecase
	releaseUsecase *usecase.ReleaseStockUsecase
}

func NewInventoryServer(reserve *usecase.ReserveStockUsecase, release *usecase.ReleaseStockUsecase) *InventoryServer {
	return &InventoryServer{
		reserveUsecase: reserve,
		releaseUsecase: release,
	}
}

// ReserveStock memotong stok di Redis secara atomik dan mencatat event ke Outbox Postgres.
func (s *InventoryServer) ReserveStock(ctx context.Context, req *pb.ReserveStockRequest) (*pb.ReserveStockResponse, error) {
	qty := req.GetQuantity()
	if qty <= 0 {
		qty = 1
	}
	err := s.reserveUsecase.Execute(ctx, req.GetProductId(), req.GetUserId(), req.GetIdempotencyKey(), int(qty))
	if err != nil {
		return &pb.ReserveStockResponse{
			Success: false,
			EventId: "",
			Message: err.Error(),
		}, nil
	}

	return &pb.ReserveStockResponse{
		Success: true,
		EventId: req.GetIdempotencyKey(),
		Message: "stock reserved",
	}, nil
}

// ReleaseStock mengembalikan stok ke Redis secara atomik via gRPC (jalur synchronous).
// Digunakan untuk pelepasan manual oleh admin atau sistem internal.
// Catatan: Pelepasan via Saga (OrderCancelledEvent Kafka) ditangani Kafka Consumer.
func (s *InventoryServer) ReleaseStock(ctx context.Context, req *pb.ReleaseStockRequest) (*pb.ReleaseStockResponse, error) {
	qty := req.GetQuantity()
	if qty <= 0 {
		qty = 1
	}
	// Gunakan product_id sebagai event_id untuk release manual jika tidak ada idempotency key eksplisit
	// Dalam penggunaan production, caller harus memberikan event_id yang unik via metadata atau field terpisah.
	// Untuk saat ini, gunakan kombinasi product_id sebagai identifier.
	eventID := req.GetProductId() // simplified — production bisa extend proto dengan event_id field

	err := s.releaseUsecase.Execute(ctx, req.GetProductId(), eventID, int(qty))
	if err != nil {
		return &pb.ReleaseStockResponse{Success: false}, nil
	}

	return &pb.ReleaseStockResponse{Success: true}, nil
}
