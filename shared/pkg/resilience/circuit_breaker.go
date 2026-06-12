package resilience

import (
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CircuitBreakerConfig adalah konfigurasi untuk Circuit Breaker.
// Semua nilai memiliki default yang masuk akal untuk Flash Sale.
type CircuitBreakerConfig struct {
	// Name adalah nama unik CB ini (untuk logging).
	Name string
	// MaxRequests adalah jumlah request yang diizinkan masuk saat CB "half-open"
	// untuk menguji apakah service downstream sudah pulih.
	MaxRequests uint32
	// Interval adalah periode evaluasi untuk mereset counter error.
	Interval time.Duration
	// Timeout adalah lamanya CB dalam state "open" sebelum mencoba "half-open".
	Timeout time.Duration
	// FailureRatio adalah rasio kegagalan (0.0–1.0) yang memicu CB "open".
	// Misal: 0.5 berarti jika 50% request gagal → CB terbuka.
	FailureRatio float64
	// MinRequests adalah jumlah minimum request sebelum failure ratio dievaluasi.
	MinRequests uint32
	// IsSuccessful adalah fungsi untuk menentukan apakah error dianggap sukses (bukan failure).
	IsSuccessful func(err error) bool
}

// DefaultCircuitBreakerConfig adalah konfigurasi default yang aman untuk
// service gRPC internal pada sistem Flash Sale.
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:         name,
		MaxRequests:  5,
		Interval:     10 * time.Second,
		Timeout:      5 * time.Second,
		FailureRatio: 0.5,
		MinRequests:  10,
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			// Pastikan gRPC business error (seperti NotFound) tidak dianggap kegagalan service
			// agar tidak men-trip Circuit Breaker saat polling dilakukan.
			if st, ok := status.FromError(err); ok {
				switch st.Code() {
				case codes.NotFound, codes.InvalidArgument, codes.AlreadyExists, codes.Unauthenticated, codes.PermissionDenied, codes.FailedPrecondition:
					return true
				}
			}
			return false
		},
	}
}

// NewCircuitBreaker membuat instance gobreaker baru dengan konfigurasi yang diberikan.
//
// Cara penggunaan:
//
//	cb := resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("inventory"))
//	result, err := cb.Execute(func() (interface{}, error) {
//	    return inventoryClient.ReserveStock(ctx, req)
//	})
func NewCircuitBreaker(cfg CircuitBreakerConfig) *gobreaker.CircuitBreaker {
	settings := gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < uint32(cfg.MinRequests) {
				return false // Belum cukup sampel
			}
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return failureRatio >= cfg.FailureRatio
		},
		IsSuccessful: cfg.IsSuccessful,
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			// State change akan di-log oleh caller menggunakan logger masing-masing.
			// Dihindari injeksi logger di sini agar package ini tetap stateless.
			_ = name
			_ = from
			_ = to
		},
	}
	return gobreaker.NewCircuitBreaker(settings)
}
