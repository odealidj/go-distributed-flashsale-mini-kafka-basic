package rest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"go-flashsale-mini-kafka-basic/api-gateway/internal/application/usecase"
	orderv1 "go-flashsale-mini-kafka-basic/proto/order/v1"
	productv1 "go-flashsale-mini-kafka-basic/proto/product/v1"
)

// --- Mocks ---

type mockProductServiceClient struct {
	ListFlashSaleProductsFunc func(ctx context.Context, page, perPage int32) (*productv1.ListFlashSaleProductsResponse, error)
}

func (m *mockProductServiceClient) ListFlashSaleProducts(ctx context.Context, page, perPage int32) (*productv1.ListFlashSaleProductsResponse, error) {
	if m.ListFlashSaleProductsFunc != nil {
		return m.ListFlashSaleProductsFunc(ctx, page, perPage)
	}
	return &productv1.ListFlashSaleProductsResponse{}, nil
}

type mockInventoryServiceClient struct {
	ReserveStockFunc func(ctx context.Context, productID, userID, eventID string) (bool, error)
}

func (m *mockInventoryServiceClient) ReserveStock(ctx context.Context, productID, userID, eventID string) (bool, error) {
	if m.ReserveStockFunc != nil {
		return m.ReserveStockFunc(ctx, productID, userID, eventID)
	}
	return true, nil
}

type mockPaymentServiceClient struct {
	ProcessPaymentFunc func(ctx context.Context, orderID string, amount int64) (bool, error)
}

func (m *mockPaymentServiceClient) ProcessPayment(ctx context.Context, orderID string, amount int64) (bool, error) {
	if m.ProcessPaymentFunc != nil {
		return m.ProcessPaymentFunc(ctx, orderID, amount)
	}
	return true, nil
}

type mockAuthServiceClient struct {
	RegisterFunc func(ctx context.Context, username, password string) (bool, error)
	LoginFunc    func(ctx context.Context, username, password string) (string, error)
}

func (m *mockAuthServiceClient) Register(ctx context.Context, username, password string) (bool, error) {
	if m.RegisterFunc != nil {
		return m.RegisterFunc(ctx, username, password)
	}
	return true, nil
}

func (m *mockAuthServiceClient) Login(ctx context.Context, username, password string) (string, error) {
	if m.LoginFunc != nil {
		return m.LoginFunc(ctx, username, password)
	}
	return "mock-access-token", nil
}

type mockOrderServiceClient struct {
	GetOrderFunc func(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error)
}

func (m *mockOrderServiceClient) GetOrder(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
	if m.GetOrderFunc != nil {
		return m.GetOrderFunc(ctx, orderID)
	}
	return &orderv1.GetOrderResponse{
		OrderId:     orderID,
		Status:      "PENDING",
		TotalAmount: 150000,
	}, nil
}

// --- Helpers ---

func generateTestToken(userID string, privateKey *rsa.PrivateKey) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": userID,
		"jti": "test-jti-uuid",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	return token.SignedString(privateKey)
}

func setupTestServer(t *testing.T) (*kratoshttp.Server, *rsa.PrivateKey, *redis.Client, *mockProductServiceClient, *mockInventoryServiceClient, *mockPaymentServiceClient, *mockAuthServiceClient, *mockOrderServiceClient) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	mockProd := &mockProductServiceClient{}
	mockInv := &mockInventoryServiceClient{}
	mockPay := &mockPaymentServiceClient{}
	mockAuth := &mockAuthServiceClient{}
	mockOrder := &mockOrderServiceClient{}

	uc := usecase.NewGatewayUsecase(mockProd, mockInv, mockPay, mockAuth, mockOrder)

	// Gunakan Redis lokal
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:16379",
	})

	srv := kratoshttp.NewServer()
	RegisterHTTPServer(srv, uc, log.DefaultLogger, &privateKey.PublicKey, rdb)

	return srv, privateKey, rdb, mockProd, mockInv, mockPay, mockAuth, mockOrder
}

// --- Test Cases ---

func TestRegister(t *testing.T) {
	srv, _, _, _, _, _, mockAuth, _ := setupTestServer(t)

	mockAuth.RegisterFunc = func(ctx context.Context, username, password string) (bool, error) {
		if username == "exists" {
			return false, fmt.Errorf("user already exists")
		}
		return true, nil
	}

	t.Run("Success", func(t *testing.T) {
		body, _ := json.Marshal(AuthRequest{Username: "newuser", Password: "password"})
		req := httptest.NewRequest("POST", "/api/v1/register", bytes.NewBuffer(body))
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}

		var resp Response
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Meta.Message != "user registered successfully" {
			t.Errorf("expected success message, got %s", resp.Meta.Message)
		}
		if resp.Meta.EventID != "" {
			t.Errorf("expected empty event_id, got %s", resp.Meta.EventID)
		}
	})

	t.Run("Failure - Already Exists", func(t *testing.T) {
		body, _ := json.Marshal(AuthRequest{Username: "exists", Password: "password"})
		req := httptest.NewRequest("POST", "/api/v1/register", bytes.NewBuffer(body))
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}

		var resp Response
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if !strings.Contains(resp.Meta.Message, "user already exists") {
			t.Errorf("expected already exists error, got %s", resp.Meta.Message)
		}
	})
}

func TestLogin(t *testing.T) {
	srv, _, _, _, _, _, mockAuth, _ := setupTestServer(t)

	mockAuth.LoginFunc = func(ctx context.Context, username, password string) (string, error) {
		if username == "admin" && password == "admin123" {
			return "token-xyz", nil
		}
		return "", fmt.Errorf("invalid credentials")
	}

	t.Run("Success", func(t *testing.T) {
		body, _ := json.Marshal(AuthRequest{Username: "admin", Password: "admin123"})
		req := httptest.NewRequest("POST", "/api/v1/login", bytes.NewBuffer(body))
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}

		var resp Response
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Meta.Message != "login successful" {
			t.Errorf("expected success message, got %s", resp.Meta.Message)
		}

		dataMap, ok := resp.Data.(map[string]any)
		if !ok || dataMap["access_token"] != "token-xyz" {
			t.Errorf("expected access_token token-xyz, got %v", resp.Data)
		}
		if resp.Meta.EventID != "" {
			t.Errorf("expected empty event_id, got %s", resp.Meta.EventID)
		}
	})
}

func TestGetProducts(t *testing.T) {
	srv, _, _, mockProd, _, _, _, _ := setupTestServer(t)

	mockProd.ListFlashSaleProductsFunc = func(ctx context.Context, page, perPage int32) (*productv1.ListFlashSaleProductsResponse, error) {
		return &productv1.ListFlashSaleProductsResponse{
			TotalItems: 2,
			Products: []*productv1.ProductItem{
				{Id: "p1", Name: "Prod 1", OriginalPrice: 100, FlashsalePrice: 50},
				{Id: "p2", Name: "Prod 2", OriginalPrice: 200, FlashsalePrice: 90},
			},
		}, nil
	}

	req := httptest.NewRequest("GET", "/api/v1/products?page=1&per_page=10", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp Response
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.Meta.Page == nil || *resp.Meta.Page != 1 {
		t.Errorf("expected page 1, got %v", resp.Meta.Page)
	}
	if resp.Meta.PerPage == nil || *resp.Meta.PerPage != 10 {
		t.Errorf("expected per_page 10, got %v", resp.Meta.PerPage)
	}
	if resp.Meta.Total == nil || *resp.Meta.Total != 2 {
		t.Errorf("expected total 2, got %v", resp.Meta.Total)
	}

	productsList, ok := resp.Data.([]any)
	if !ok || len(productsList) != 2 {
		t.Errorf("expected 2 products, got %v", resp.Data)
	}
}

func TestCheckout(t *testing.T) {
	srv, privateKey, _, _, mockInv, _, _, _ := setupTestServer(t)

	token, _ := generateTestToken("usr_1", privateKey)

	t.Run("Success - Accepted", func(t *testing.T) {
		mockInv.ReserveStockFunc = func(ctx context.Context, productID, userID, eventID string) (bool, error) {
			return true, nil
		}

		body, _ := json.Marshal(CheckoutRequest{ProductID: "p1"})
		req := httptest.NewRequest("POST", "/api/v1/checkout", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Idempotency-Key", "idemp-key-test")
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Errorf("expected 202, got %d", rec.Code)
		}

		var resp Response
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Meta.Message != "pesanan sedang diproses" {
			t.Errorf("expected message, got %s", resp.Meta.Message)
		}
		if resp.Meta.EventID != "idemp-key-test" {
			t.Errorf("expected event_id, got %s", resp.Meta.EventID)
		}

		dataMap, ok := resp.Data.(map[string]any)
		if !ok || dataMap["order_id"] != "idemp-key-test" {
			t.Errorf("expected order_id idemp-key-test in data, got %v", resp.Data)
		}
	})

	t.Run("Failure - Insufficient Stock", func(t *testing.T) {
		mockInv.ReserveStockFunc = func(ctx context.Context, productID, userID, eventID string) (bool, error) {
			return false, nil
		}

		body, _ := json.Marshal(CheckoutRequest{ProductID: "p1"})
		req := httptest.NewRequest("POST", "/api/v1/checkout", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Idempotency-Key", "idemp-key-test")
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rec.Code)
		}

		var resp Response
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Meta.Message != "stok habis atau sedang diproses" {
			t.Errorf("expected error message, got %s", resp.Meta.Message)
		}
	})
}

func TestCheckoutLongPolling(t *testing.T) {
	srv, privateKey, _, _, mockInv, _, _, mockOrder := setupTestServer(t)

	token, _ := generateTestToken("usr_1", privateKey)

	t.Run("Success", func(t *testing.T) {
		mockInv.ReserveStockFunc = func(ctx context.Context, productID, userID, eventID string) (bool, error) {
			return true, nil
		}
		mockOrder.GetOrderFunc = func(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
			return &orderv1.GetOrderResponse{
				OrderId:     orderID,
				Status:      "PENDING",
				TotalAmount: 150000,
			}, nil
		}

		body, _ := json.Marshal(CheckoutRequest{ProductID: "p1"})
		req := httptest.NewRequest("POST", "/api/v1/checkout/long-polling", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Idempotency-Key", "idemp-lp-test")
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}

		var resp Response
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Meta.Message != "success" {
			t.Errorf("expected success, got %s", resp.Meta.Message)
		}
		if resp.Meta.EventID != "idemp-lp-test" {
			t.Errorf("expected event_id, got %s", resp.Meta.EventID)
		}

		dataMap, ok := resp.Data.(map[string]any)
		if !ok || dataMap["order_id"] != "idemp-lp-test" {
			t.Errorf("expected order_id, got %v", resp.Data)
		}
	})
}

func TestCheckoutSSE(t *testing.T) {
	srv, privateKey, _, _, mockInv, _, _, mockOrder := setupTestServer(t)

	token, _ := generateTestToken("usr_1", privateKey)

	mockInv.ReserveStockFunc = func(ctx context.Context, productID, userID, eventID string) (bool, error) {
		return true, nil
	}
	mockOrder.GetOrderFunc = func(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
		return &orderv1.GetOrderResponse{
			OrderId:     orderID,
			Status:      "PENDING",
			TotalAmount: 150000,
		}, nil
	}

	body, _ := json.Marshal(CheckoutRequest{ProductID: "p1"})
	req := httptest.NewRequest("POST", "/api/v1/checkout/sse", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Idempotency-Key", "idemp-sse-test")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	bodyStr := rec.Body.String()
	if !strings.Contains(bodyStr, "data:") {
		t.Errorf("expected data in SSE stream, got %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"event_id":"idemp-sse-test"`) {
		t.Errorf("expected event_id in SSE json data, got %s", bodyStr)
	}
}

func TestGetOrder(t *testing.T) {
	srv, privateKey, _, _, _, _, _, mockOrder := setupTestServer(t)

	token, _ := generateTestToken("usr_1", privateKey)

	mockOrder.GetOrderFunc = func(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
		return &orderv1.GetOrderResponse{
			OrderId:     "order_123",
			Status:      "PENDING",
			TotalAmount: 250000,
		}, nil
	}

	req := httptest.NewRequest("GET", "/api/v1/orders/order_123", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp Response
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.Meta.EventID != "order_123" {
		t.Errorf("expected event_id order_123, got %s", resp.Meta.EventID)
	}

	dataMap, ok := resp.Data.(map[string]any)
	if !ok || dataMap["order_id"] != "order_123" || dataMap["status"] != "PENDING" {
		t.Errorf("expected order data, got %v", resp.Data)
	}
}

func TestCheckoutPubSub(t *testing.T) {
	srv, privateKey, rdb, _, mockInv, _, _, mockOrder := setupTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Check if Redis is up, otherwise skip this test
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("local Redis not running, skipping Redis Pub/Sub test case")
	}

	token, _ := generateTestToken("usr_1", privateKey)

	mockInv.ReserveStockFunc = func(ctx context.Context, productID, userID, eventID string) (bool, error) {
		return true, nil
	}
	mockOrder.GetOrderFunc = func(ctx context.Context, orderID string) (*orderv1.GetOrderResponse, error) {
		return &orderv1.GetOrderResponse{
			OrderId:     orderID,
			Status:      "PENDING",
			TotalAmount: 150000,
		}, nil
	}

	idempKey := "idemp-pubsub-test"

	// Publish status COMPLETED after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		rdb.Publish(context.Background(), "order:status:"+idempKey, "COMPLETED")
	}()

	body, _ := json.Marshal(CheckoutRequest{ProductID: "p1"})
	req := httptest.NewRequest("POST", "/api/v1/checkout/pubsub", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Idempotency-Key", idempKey)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp Response
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.Meta.Message != "success (pubsub)" {
		t.Errorf("expected message success (pubsub), got %s", resp.Meta.Message)
	}
	if resp.Meta.EventID != idempKey {
		t.Errorf("expected event_id, got %s", resp.Meta.EventID)
	}

	dataMap, ok := resp.Data.(map[string]any)
	if !ok || dataMap["order_id"] != idempKey || dataMap["status"] != "COMPLETED" {
		t.Errorf("expected status COMPLETED, got %v", resp.Data)
	}
}
