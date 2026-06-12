package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go-flashsale-mini-kafka-basic/auth-service/internal/application/usecase"
	pb "go-flashsale-mini-kafka-basic/proto/auth/v1"
)

type AuthServer struct {
	pb.UnimplementedAuthServiceServer
	usecase *usecase.AuthUsecase
}

func NewAuthServer(uc *usecase.AuthUsecase) *AuthServer {
	return &AuthServer{usecase: uc}
}

func (s *AuthServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	success, err := s.usecase.Register(ctx, req.Username, req.Password)
	if err != nil {
		return &pb.RegisterResponse{Success: false, Message: err.Error()}, status.Errorf(codes.Internal, "failed to register: %v", err)
	}

	return &pb.RegisterResponse{Success: success, Message: "user registered successfully"}, nil
}

func (s *AuthServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	token, err := s.usecase.Login(ctx, req.Username, req.Password)
	if err != nil {
		return &pb.LoginResponse{Success: false, Message: err.Error()}, status.Errorf(codes.Unauthenticated, "failed to login: %v", err)
	}

	return &pb.LoginResponse{Success: true, AccessToken: token, Message: "login successful"}, nil
}
