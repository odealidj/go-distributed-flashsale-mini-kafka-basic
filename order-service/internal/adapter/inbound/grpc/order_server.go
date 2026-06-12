package grpc

import (
	"context"
	"database/sql"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go-flashsale-mini-kafka-basic/order-service/internal/application/port"
	pb "go-flashsale-mini-kafka-basic/proto/order/v1"
)

type OrderServer struct {
	pb.UnimplementedOrderServiceServer
	repo port.OrderRepository
}

func NewOrderServer(repo port.OrderRepository) *OrderServer {
	return &OrderServer{repo: repo}
}

func (s *OrderServer) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.GetOrderResponse, error) {
	order, err := s.repo.GetOrder(ctx, req.GetOrderId())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || err.Error() == "sql: no rows in result set" {
			return nil, status.Error(codes.NotFound, "order not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get order: %v", err)
	}

	return &pb.GetOrderResponse{
		OrderId:     order.ID,
		Status:      order.Status,
		TotalAmount: order.TotalAmount,
	}, nil
}
