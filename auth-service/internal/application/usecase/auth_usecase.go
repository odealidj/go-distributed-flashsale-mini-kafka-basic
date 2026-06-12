package usecase

import (
	"context"
	"crypto/rsa"
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"go-flashsale-mini-kafka-basic/auth-service/internal/application/domain"
)

type UserRepository interface {
	CreateUser(ctx context.Context, user *domain.User) error
	GetUserByUsername(ctx context.Context, username string) (*domain.User, error)
}

type AuthUsecase struct {
	userRepo   UserRepository
	privateKey *rsa.PrivateKey
}

func NewAuthUsecase(userRepo UserRepository) (*AuthUsecase, error) {
	keyPath := os.Getenv("JWT_PRIVATE_KEY_PATH")
	if keyPath == "" {
		return nil, errors.New("JWT_PRIVATE_KEY_PATH is not set")
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(keyBytes)
	if err != nil {
		return nil, err
	}

	return &AuthUsecase{
		userRepo:   userRepo,
		privateKey: privateKey,
	}, nil
}

func (u *AuthUsecase) Register(ctx context.Context, username, password string) (bool, error) {
	// Check if exists
	existing, err := u.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, errors.New("username already exists")
	}

	// Hash password
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}

	user := &domain.User{
		ID:           "usr_" + uuid.New().String()[:8], // Simplified ID
		Username:     username,
		PasswordHash: string(hashed),
	}

	err = u.userRepo.CreateUser(ctx, user)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (u *AuthUsecase) Login(ctx context.Context, username, password string) (string, error) {
	user, err := u.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", errors.New("invalid credentials")
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return "", errors.New("invalid credentials")
	}

	// Generate JWT
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": user.ID,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(15 * time.Minute).Unix(),
		"jti": uuid.New().String(), // Unique token ID for logout/blacklist
	})

	tokenString, err := token.SignedString(u.privateKey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}
