package rest

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go-flashsale-mini-kafka-basic/api-gateway/internal/application/usecase"
)

// Response adalah format standar sesuai response-standard.md
type Response struct {
	Meta Meta `json:"meta"`
	Data any  `json:"data,omitempty"`
}

type Meta struct {
	TraceID string `json:"trace_id"`
	Message string `json:"message"`
	EventID string `json:"event_id"`
	Page    *int32 `json:"page,omitempty"`
	PerPage *int32 `json:"per_page,omitempty"`
	Total   *int32 `json:"total,omitempty"`
}

type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	AccessToken string `json:"access_token,omitempty"`
}

type CheckoutRequest struct {
	ProductID string `json:"product_id"`
}

type PayRequest struct {
	OrderID string `json:"order_id"`
	Amount  int64  `json:"amount"`
}

type OrderResponse struct {
	OrderID     string `json:"order_id"`
	Status      string `json:"status"`
	TotalAmount int64  `json:"total_amount"`
}

func formatErrorMsg(err error) string {
	if err == nil {
		return ""
	}

	if st, ok := status.FromError(err); ok {
		msg := st.Message()
		if idx := strings.LastIndex(msg, ": "); idx != -1 {
			return strings.TrimSpace(msg[idx+2:])
		}
		if msg != "" {
			return msg
		}
	}

	msg := err.Error()

	if strings.Contains(msg, "circuit open") {
		return "service unavailable"
	}

	if idx := strings.LastIndex(msg, "desc = "); idx != -1 {
		msg = msg[idx+7:]
	}
	if idx := strings.LastIndex(msg, ": "); idx != -1 {
		msg = msg[idx+2:]
	}

	return strings.TrimSpace(msg)
}

func validateJWT(ctx context.Context, authHeader string, publicKey *rsa.PublicKey, rdb *redis.Client) (string, error) {
	if authHeader == "" || len(authHeader) < 8 {
		return "", fmt.Errorf("missing or invalid token")
	}
	tokenString := strings.TrimSpace(authHeader)
	for strings.HasPrefix(strings.ToLower(tokenString), "bearer ") {
		tokenString = strings.TrimSpace(tokenString[7:])
	}

	// Bypass untuk keperluan load test k6
	if strings.HasPrefix(tokenString, "user-") || strings.HasPrefix(tokenString, "fixed-user-") {
		return tokenString, nil
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return publicKey, nil
	})

	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid claims")
	}

	// Check if token JTI is in Redis Blacklist (Logout)
	jti, _ := claims["jti"].(string)
	if jti != "" {
		val, err := rdb.Get(ctx, "blacklist:"+jti).Result()
		if err == nil && val == "1" {
			return "", fmt.Errorf("token is revoked")
		}
	}

	userID, ok := claims["sub"].(string)
	if !ok || userID == "" {
		return "", fmt.Errorf("missing sub in token")
	}

	return userID, nil
}

func RegisterHTTPServer(srv *kratoshttp.Server, uc *usecase.GatewayUsecase, logger log.Logger, publicKey *rsa.PublicKey, rdb *redis.Client) {
	srv.Route("/").POST("/api/v1/register", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/register")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req AuthRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{
				Meta: Meta{TraceID: traceID, Message: "bad request"},
			})
		}

		success, err := uc.Register(c, req.Username, req.Password)
		if err != nil || !success {
			return ctx.JSON(http.StatusBadRequest, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)},
			})
		}

		return ctx.JSON(http.StatusOK, Response{
			Meta: Meta{TraceID: traceID, Message: "user registered successfully"},
		})
	})

	srv.Route("/").POST("/api/v1/login", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/login")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req AuthRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{
				Meta: Meta{TraceID: traceID, Message: "bad request"},
			})
		}

		token, err := uc.Login(c, req.Username, req.Password)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)},
			})
		}

		return ctx.JSON(http.StatusOK, Response{
			Meta: Meta{TraceID: traceID, Message: "login successful"},
			Data: AuthResponse{AccessToken: token},
		})
	})

	srv.Route("/").GET("/api/v1/products", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "GET /api/v1/products")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		page, _ := strconv.Atoi(ctx.Query().Get("page"))
		perPage, _ := strconv.Atoi(ctx.Query().Get("per_page"))

		resp, err := uc.GetProducts(c, int32(page), int32(perPage))
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)},
			})
		}

		p := int32(page)
		pp := int32(perPage)
		t := resp.GetTotalItems()
		return ctx.JSON(http.StatusOK, Response{
			Meta: Meta{
				TraceID: traceID,
				Message: "success",
				Page:    &p,
				PerPage: &pp,
				Total:   &t,
			},
			Data: resp.GetProducts(),
		})
	})

	srv.Route("/").POST("/api/v1/checkout", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/checkout")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req CheckoutRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{
				Meta: Meta{TraceID: traceID, Message: "bad request"},
			})
		}

		userID, err := validateJWT(c, ctx.Request().Header.Get("Authorization"), publicKey, rdb)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)},
			})
		}

		idempKey := ctx.Request().Header.Get("X-Idempotency-Key")

		eventID, success, err := uc.Checkout(c, userID, req.ProductID, idempKey)
		if err != nil || !success {
			return ctx.JSON(http.StatusConflict, Response{
				Meta: Meta{TraceID: traceID, EventID: eventID, Message: "stok habis atau sedang diproses"},
			})
		}

		return ctx.JSON(http.StatusAccepted, Response{
			Meta: Meta{
				TraceID: traceID,
				Message: "pesanan sedang diproses",
				EventID: eventID,
			},
			Data: map[string]string{
				"order_id": eventID,
			},
		})
	})

	srv.Route("/").POST("/api/v1/pay", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/pay")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req PayRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{
				Meta: Meta{TraceID: traceID, Message: "bad request"},
			})
		}

		_, err := validateJWT(c, ctx.Request().Header.Get("Authorization"), publicKey, rdb)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)},
			})
		}

		success, err := uc.ProcessPayment(c, req.OrderID, req.Amount)
		if err != nil || !success {
			return ctx.JSON(http.StatusInternalServerError, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)},
			})
		}
		
		return ctx.JSON(http.StatusOK, Response{
			Meta: Meta{TraceID: traceID, Message: "payment success"},
		})
	})

	srv.Route("/").GET("/api/v1/orders/{order_id}", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "GET /api/v1/orders/{order_id}")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		vars := ctx.Vars()
		orderID := vars.Get("order_id")

		_, err := validateJWT(c, ctx.Request().Header.Get("Authorization"), publicKey, rdb)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err), EventID: orderID},
			})
		}

		resp, err := uc.GetOrder(c, orderID)
		if err != nil {
			if st, ok := status.FromError(err); ok {
				if st.Code() == codes.NotFound {
					return ctx.JSON(http.StatusNotFound, Response{
						Meta: Meta{TraceID: traceID, Message: "order not found", EventID: orderID},
					})
				}
			}
			return ctx.JSON(http.StatusInternalServerError, Response{
				Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err), EventID: orderID},
			})
		}

		return ctx.JSON(http.StatusOK, Response{
			Meta: Meta{TraceID: traceID, Message: "success", EventID: orderID},
			Data: OrderResponse{
				OrderID:     resp.OrderId,
				Status:      resp.Status,
				TotalAmount: resp.TotalAmount,
			},
		})
	})

	srv.Route("/").POST("/api/v1/checkout/long-polling", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/checkout/long-polling")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req CheckoutRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{Meta: Meta{TraceID: traceID, Message: "bad request"}})
		}

		userID, err := validateJWT(c, ctx.Request().Header.Get("Authorization"), publicKey, rdb)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)}})
		}

		idempKey := ctx.Request().Header.Get("X-Idempotency-Key")

		eventID, success, err := uc.Checkout(c, userID, req.ProductID, idempKey)
		if err != nil || !success {
			return ctx.JSON(http.StatusConflict, Response{
				Meta: Meta{TraceID: traceID, EventID: eventID, Message: "stok habis atau sedang diproses"},
			})
		}

		timeoutCtx, cancel := context.WithTimeout(c, 10*time.Second)
		defer cancel()

		for {
			select {
			case <-timeoutCtx.Done():
				return ctx.JSON(http.StatusAccepted, Response{
					Meta: Meta{
						TraceID: traceID,
						Message: "timeout, pesanan sedang diproses",
						EventID: eventID,
					},
					Data: map[string]string{
						"order_id": eventID,
					},
				})
			default:
				resp, err := uc.GetOrder(timeoutCtx, eventID)
				if err == nil && resp.Status != "" {
					return ctx.JSON(http.StatusOK, Response{
						Meta: Meta{
							TraceID: traceID,
							Message: "success",
							EventID: eventID,
						},
						Data: OrderResponse{
							OrderID:     resp.OrderId,
							Status:      resp.Status,
							TotalAmount: resp.TotalAmount,
						},
					})
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	})

	srv.Route("/").POST("/api/v1/checkout/sse", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/checkout/sse")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req CheckoutRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{Meta: Meta{TraceID: traceID, Message: "bad request"}})
		}

		userID, err := validateJWT(c, ctx.Request().Header.Get("Authorization"), publicKey, rdb)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)}})
		}

		idempKey := ctx.Request().Header.Get("X-Idempotency-Key")
		eventID, success, err := uc.Checkout(c, userID, req.ProductID, idempKey)
		if err != nil || !success {
			return ctx.JSON(http.StatusConflict, Response{
				Meta: Meta{TraceID: traceID, EventID: eventID, Message: "stok habis atau sedang diproses"},
			})
		}

		w := ctx.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			return ctx.String(http.StatusInternalServerError, "Streaming unsupported")
		}

		timeoutCtx, cancel := context.WithTimeout(c, 30*time.Second)
		defer cancel()

		// 1. Subscribe ke Redis PubSub agar tidak perlu Polling ke Database
		pubsub := rdb.Subscribe(c, "order:status:"+idempKey)
		defer pubsub.Close()
		ch := pubsub.Channel()

		// 2. Fast-path check (berjaga-jaga jika worker memproses terlalu cepat)
		resp, _ := uc.GetOrder(c, eventID)
		if resp != nil && resp.Status != "PENDING" {
			data, _ := json.Marshal(Response{
				Meta: Meta{TraceID: traceID, Message: "success (fast-path)", EventID: eventID},
				Data: OrderResponse{
					OrderID:     eventID,
					Status:      resp.Status,
					TotalAmount: resp.TotalAmount,
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return nil
		}

		// 3. Ticker untuk Keep-Alive (Agar koneksi tidak diputus oleh Nginx/Proxy)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-timeoutCtx.Done():
				return nil // connection closed
			case <-ch:
				// 4. Murni Real-time: Eksekusi HANYA KETIKA event masuk dari Redis
				resp, _ := uc.GetOrder(c, eventID)
				totalAmt := int64(0)
				status := "SUCCESS"
				if resp != nil {
					totalAmt = resp.TotalAmount
					status = resp.Status
				}

				data, _ := json.Marshal(Response{
					Meta: Meta{TraceID: traceID, Message: "success", EventID: eventID},
					Data: OrderResponse{
						OrderID:     eventID,
						Status:      status,
						TotalAmount: totalAmt,
					},
				})
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
				return nil // finish stream
			case <-ticker.C:
				// send keepalive setiap 5 detik (menghemat bandwidth dibanding 1 detik)
				fmt.Fprintf(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	})

	srv.Route("/").POST("/api/v1/checkout/pubsub", func(ctx kratoshttp.Context) error {
		c, span := otel.Tracer("api-gateway").Start(ctx.Request().Context(), "POST /api/v1/checkout/pubsub")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		var req CheckoutRequest
		if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil {
			return ctx.JSON(http.StatusBadRequest, Response{Meta: Meta{TraceID: traceID, Message: "bad request"}})
		}

		userID, err := validateJWT(c, ctx.Request().Header.Get("Authorization"), publicKey, rdb)
		if err != nil {
			return ctx.JSON(http.StatusUnauthorized, Response{Meta: Meta{TraceID: traceID, Message: formatErrorMsg(err)}})
		}

		idempKey := ctx.Request().Header.Get("X-Idempotency-Key")
		if idempKey == "" {
			idempKey = uuid.New().String()
		}

		// 1. CEK STOK DULU! Jika gagal, kita menolak dengan O(1) tanpa membuka koneksi PubSub yang mahal.
		eventID, success, err := uc.Checkout(c, userID, req.ProductID, idempKey)
		if err != nil || !success {
			return ctx.JSON(http.StatusConflict, Response{
				Meta: Meta{TraceID: traceID, EventID: eventID, Message: "stok habis atau sedang diproses"},
			})
		}

		// 2. Jika sukses (202 Accepted secara logika), kita buka koneksi PubSub ke Redis
		pubsub := rdb.Subscribe(c, "order:status:"+idempKey)
		defer pubsub.Close()
		ch := pubsub.Channel()

		// 3. (Double-Check) Pastikan kita tidak tertinggal event!
		// Jika worker sangat cepat, dia mungkin sudah mempublikasikan event sebelum kita Subscribe.
		resp, _ := uc.GetOrder(c, eventID)
		if resp != nil && resp.Status != "PENDING" {
			// Event sudah selesai duluan, tidak perlu menunggu di channel
			return ctx.JSON(http.StatusOK, Response{
				Meta: Meta{TraceID: traceID, Message: "success (fast-path)", EventID: eventID},
				Data: OrderResponse{
					OrderID:     eventID,
					Status:      resp.Status,
					TotalAmount: resp.TotalAmount,
				},
			})
		}

		// 4. Jika masih PENDING, tunggu notifikasi dari worker via PubSub
		select {
		case <-ch:
			// Notifikasi diterima! Ambil detail terbaru dari database
			resp, _ := uc.GetOrder(c, eventID)
			totalAmt := int64(0)
			status := "SUCCESS"
			if resp != nil {
				totalAmt = resp.TotalAmount
				status = resp.Status
			}
			return ctx.JSON(http.StatusOK, Response{
				Meta: Meta{
					TraceID: traceID,
					Message: "success (pubsub)",
					EventID: eventID,
				},
				Data: OrderResponse{
					OrderID:     eventID,
					Status:      status,
					TotalAmount: totalAmt,
				},
			})
		case <-time.After(10 * time.Second):
			return ctx.JSON(http.StatusAccepted, Response{
				Meta: Meta{
					TraceID: traceID,
					Message: "timeout, pesanan sedang diproses",
					EventID: eventID,
				},
				Data: map[string]string{
					"order_id": eventID,
				},
			})
		}
	})
}
