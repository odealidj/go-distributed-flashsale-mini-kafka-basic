// Package resilience menyediakan pola ketahanan sistem (resilience patterns)
// yang dapat digunakan oleh semua service dalam monorepo ini.
//
// Pola yang tersedia:
//   - Circuit Breaker (menggunakan sony/gobreaker)
//   - gRPC call timeout wrapper
//   - Retry dengan exponential backoff
package resilience
