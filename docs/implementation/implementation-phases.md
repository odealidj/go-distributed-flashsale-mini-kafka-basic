# Phase Implementasi (Flash Sale System)

## 1. Tujuan
Dokumen ini membagi pekerjaan pembuatan 5 *microservices* + API Gateway untuk sistem Flash Sale menjadi fase-fase yang jelas, *incremental*, dan terstruktur. Pendekatan ini memastikan kita bisa memvalidasi logika inti (Redis Lua Script & Kafka Saga) secepat mungkin sebelum mempercantik UI atau menambah fitur sekunder.

## 2. Definition of Done Umum
Sebuah fase dianggap selesai jika:
*   Kode mengikuti *Hexagonal Architecture*.
*   REST Response API Gateway mengikuti `docs/api/response-standard.md`.
*   Operasi database menggunakan `sqlx` untuk *struct scanning* dengan raw SQL.
*   Semua perubahan berhasil dijalankan secara lokal via `docker-compose` (infra) + `go run` (services).

---

## 3. Fase 01 - Fondasi Monorepo & Infrastruktur
**Target:** Menyiapkan kerangka kerja kosong namun bisa di-*build*.
*   Inisialisasi `go.work` di root dan `go.mod` di 6 service (`api-gateway`, `auth-service`, `inventory-service`, `order-service`, `payment-service`, `product-service`) plus `shared` dan `proto`.
*   Membuat `docker-compose.yml` untuk PostgreSQL, Redis, Kafka (dengan Kafka UI), Jaeger, dan Reverse Proxy (NGINX).
*   Menyiapkan skrip inisialisasi database (`init-dbs.sh`) di root proyek — membuat 5 database terpisah (`db_product`, `db_inventory`, `db_order`, `db_payment`, `db_auth`).
*   Membuat *proto files* (gRPC contracts) untuk semua komunikasi internal.

## 4. Fase 02 - Katalog Produk & Inventory (Core Flash Sale)
**Target:** Fitur paling kritikal, yaitu memotong stok dengan kecepatan cahaya, dapat berfungsi secara independen.
*   **Product Service:** Membuat REST/gRPC endpoint untuk membaca katalog produk. Caching menggunakan Redis.
*   **Inventory Service:** Mengimplementasikan **Redis Lua Script** untuk atomic counter (Reserve Stock). Jika stok di Redis berhasil dipotong, buat transaksi *Outbox* di PostgreSQL untuk secara async mengirim pesan ke Kafka.
*   Endpoint testing independen untuk memborbardir Inventory Service.

## 5. Fase 03 - API Gateway & Reverse Proxy
**Target:** Menyediakan satu pintu masuk terpusat untuk klien eksternal.
*   Setup *Reverse Proxy* (NGINX) untuk *rate-limiting* (`limit_req_zone` 10 req/s per IP, burst 20).
*   **API Gateway:** Membuat BFF (*Backend for Frontend*) menggunakan Go (Kratos). Menerima *request HTTP REST*, memvalidasi Auth (opsional), lalu mengonversinya menjadi panggilan `gRPC` ke *Product Service* dan *Inventory Service*.
*   Format response di-standarisasi di sini.

## 6. Fase 04 - Order Core & Payment (Saga Choreography)
**Target:** Mengimplementasikan mesin *state* (Saga) berbasis *Event-Driven* Kafka.
*   **Order Service:** Mendengarkan Kafka (Kafka Consumer via `franz-go`). Saat menerima `StockReservedEvent` dari Inventory, simpan pesanan dengan status `PENDING_PAYMENT`.
*   **Payment Service:** Membuat endpoint gRPC `/pay` (dipanggil oleh API Gateway). Mensimulasikan sukses/gagal bayar. Jika sukses, lempar `PaymentCompletedEvent` ke Kafka.
*   **Order Service (Compensation):** Jika *Order Service* menyadari pesanan kadaluwarsa (15 menit), lempar `OrderCancelledEvent`. *Inventory Service* mendengar ini dan mengembalikan stok ke Redis.

## 7. Fase 05 - Observability (Tracing & Idempotency)
**Target:** Mencegah masalah sistem terdistribusi (duplikasi pesan & sulit dilacak).
*   Penerapan *Idempotency Key* di setiap *Kafka Consumer* (menggunakan tabel `processed_events` di Postgres) agar tidak ada pesanan ganda jika Kafka mengirim ulang pesan (at-least-once delivery).
*   Injeksi *OpenTelemetry* Trace ID dari API Gateway, diteruskan ke Metadata gRPC, hingga masuk ke Header Kafka agar satu *checkout* bisa divisualisasikan dari ujung ke ujung di Jaeger.

## 8. Fase 06 - Performance Testing (K6)
**Target:** Membuktikan arsitektur tahan menghadapi *Thundering Herd Problem*.
*   Membuat *script* k6 di folder `performance-tests/k6/` untuk mensimulasikan ratusan *concurrent users* mencoba membeli stok barang terbatas dalam detik yang sama.
*   Verifikasi bahwa *Redis Lua Script* sukses mencegah stok minus (*overselling*).
*   Verifikasi bahwa *API Gateway* dapat merespons di bawah 200ms meskipun Kafka di-*backend* sibuk memproses pesanan secara asinkron.

## 9. Fase 07 - Resilience (Ketahanan Sistem)
**Target:** Memastikan kegagalan satu komponen tidak merembet ke seluruh sistem (*cascading failure*).

### 9.1 Pola yang Diimplementasikan

#### Circuit Breaker (API Gateway → gRPC Services)
*   **Library:** `github.com/sony/gobreaker`
*   **Lokasi:** `shared/pkg/resilience/circuit_breaker.go` + `api-gateway/internal/adapter/outbound/grpc/clients.go`
*   **Konfigurasi default:** Terbuka jika 50% dari 10 request terakhir gagal. Coba tutup (*half-open*) setelah 5 detik.
*   **Isolasi:** Circuit Breaker **terpisah** per service downstream (product, inventory, payment). Kegagalan inventory tidak mematikan payment.
*   **Error khusus:** `gobreaker.ErrOpenState` dikembalikan saat CB terbuka, diterjemahkan menjadi HTTP 503.

#### Timeout Per-Call gRPC
*   **Lokasi:** `api-gateway/internal/adapter/outbound/grpc/clients.go`
*   **Nilai:** 3 detik per panggilan gRPC (harus < timeout Nginx/upstream).
*   **Implementasi:** `context.WithTimeout(ctx, 3*time.Second)` di setiap method.

#### gRPC Keepalive
*   **Lokasi:** `api-gateway/internal/adapter/outbound/grpc/clients.go`
*   **Parameter:** Ping setiap 10 detik, timeout 5 detik.
*   **Fungsi:** Mendeteksi koneksi mati (dead connection) tanpa menunggu request berikutnya gagal.

#### Retry dengan Exponential Backoff + Jitter
*   **Library:** Pure Go, tanpa dependensi eksternal.
*   **Lokasi:** `shared/pkg/resilience/retry.go`
*   **Default:** 3 percobaan, interval awal 100ms, multiplier 2x, max 2 detik, ±30% jitter.
*   **CATATAN PENTING:** Retry **TIDAK** digunakan untuk `ReserveStock` karena operasi ini memotong stok di Redis. Retry bisa menyebabkan pemotongan ganda jika event_id berbeda. Idempotency dijaga oleh Redis Lua Script via `idempotency_key`.
*   Retry digunakan untuk: Outbox Relay publish ke Kafka.

#### Outbox Relay Worker — Retry + Status FAILED (3 Instances)
*   **Lokasi:** `shared/pkg/outbox/relay.go` (Inti Worker), dijalankan di `inventory-service`, `order-service`, dan `payment-service`.
*   **Perubahan:** Karena tabel `outbox_messages` kini dipisah di 3 database, kita menjalankan 3 Relay Worker terpisah secara paralel. Jika publish ke Kafka gagal setelah 5 retry, baris ditandai `status = 'FAILED'` (bukan hilang).
*   **RequiredAcks:** `kgo.AllISRAcks()` — Kafka hanya dianggap berhasil menerima jika semua ISR (in-sync replica) mengkonfirmasi.
*   **Transaksi:** Seluruh batch polling dibungkus transaksi `FOR UPDATE SKIP LOCKED`.

#### Kafka Consumer — Manual Commit + Dead Letter Queue (DLQ)
*   **Lokasi:** `order-service/internal/adapter/inbound/kafka/consumer.go`
*   **Manual Commit:** `DisableAutoCommit()`. Offset hanya di-commit setelah pemrosesan sukses atau setelah dikirim ke DLQ.
*   **DLQ Topic:** `flashsale.order.dlq`
*   **Metadata DLQ:** Setiap pesan DLQ menyertakan header: `dlq.original.topic`, `dlq.error`, `dlq.timestamp`.
*   **Retry Consumer:** 3x dengan backoff 500ms–5s sebelum dikirim ke DLQ.

#### Database Connection Pool
*   **Library:** `shared/pkg/database/postgres.go`
*   **Nilai default:** `MaxOpenConns=25`, `MaxIdleConns=10`, `ConnMaxLifetime=5m`, `ConnMaxIdleTime=2m`.
*   **Tujuan:** Mencegah connection pool exhaustion saat thundering herd, dan membuang koneksi basi yang mungkin ditutup sisi server.

#### Rate Limiting via Nginx
*   **Lokasi:** `nginx.conf` (sudah ada)
*   **Konfigurasi:** `limit_req_zone` 10 req/s per IP, burst 20 dengan `nodelay`, HTTP 429 jika terlampaui.

### 9.2 Lokasi File Baru
| File | Deskripsi |
|------|-----------|
| `shared/pkg/resilience/circuit_breaker.go` | Circuit Breaker wrapper (sony/gobreaker) |
| `shared/pkg/resilience/retry.go` | Retry + exponential backoff + jitter |
| `shared/pkg/resilience/doc.go` | Dokumentasi package |
| `shared/pkg/database/postgres.go` | DB connection pool helper |

### 9.3 File yang Dimodifikasi
| File | Perubahan |
|------|-----------|
| `api-gateway/internal/adapter/outbound/grpc/clients.go` | + CB per service + timeout + keepalive |
| `shared/pkg/outbox/relay.go` | + retry, RequiredAcks, status FAILED |
| `order-service/internal/adapter/inbound/kafka/consumer.go` | + DLQ, manual commit, retry |

### 9.4 Apa yang Sengaja Tidak Di-Retry
| Operasi | Alasan |
|---------|--------|
| `ReserveStock` (gRPC) | Non-idempoten untuk stok. Idempotency via Redis event_id. |
| Parsing payload Kafka | Permanent error — payload corrupt tidak akan sembuh dengan retry. |

---

## 10. Fase 08 - Saga Compensation (Unhappy Path)
**Target:** Melengkapi siklus transaksi untuk sisi kegagalan (*Unhappy Path*).
*   **Payment Service:** Mensimulasikan kegagalan pembayaran jika `amount % 10 == 4`. Menerbitkan `PaymentFailedEvent` ke Outbox.
*   **Order Service (Saga):** Menangani `PaymentFailedEvent` → set status `CANCELLED` dan menerbitkan `OrderCancelledEvent`.
*   **Order Service (Timeout Worker):** Goroutine ticker setiap 30 detik yang membatalkan pesanan `PENDING` > 15 menit menggunakan `FOR UPDATE SKIP LOCKED`.
*   **Inventory Service (Consumer):** Kafka Consumer baru mendengarkan `flashsale.order.events`. Jika menerima `OrderCancelledEvent`, mengembalikan stok via Redis Lua Script (`RefundStockScript`: INCRBY + DEL idempotency key).
*   Consumer dilengkapi DLQ (`flashsale.inventory.dlq`) dan Exponential Backoff Retry.

## 11. Fase 09 - Automated Testing & CI Pipeline
**Target:** Menambahkan *Unit Test* terotomasi dan *Continuous Integration*.
*   **Unit Testing:** Menggunakan `testify` untuk asersi dan *mocking* (pola AAA: Arrange-Act-Assert).
    *   `ProcessPaymentUsecase` — Skenario Sukses dan Gagal.
    *   `OrderSagaUsecase` — HandleStockReserved, HandlePaymentCompleted, HandlePaymentFailed.
*   **Mock Repository:** Manual mock menggunakan `testify/mock` untuk `OrderRepository` dan `PaymentRepository`.
*   **CI Pipeline:** GitHub Actions (`.github/workflows/ci.yml`) yang menjalankan `golangci-lint` dan `go test -cover` pada setiap `push` dan `pull_request`.

