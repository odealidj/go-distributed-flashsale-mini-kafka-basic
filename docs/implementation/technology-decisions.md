# Keputusan Teknologi (Tech Stack)

Dokumen ini mendokumentasikan pustaka (*library*) dan teknologi yang dipilih untuk implementasi proyek Flash Sale, beserta alasan di baliknya.

## 1. Bahasa & Workspace
*   **Go (1.21+)**: Dipilih karena kinerja konkurensi (goroutine) yang sangat efisien, konsumsi memori rendah, dan ekosistem *cloud-native* yang kuat. Sangat krusial untuk menahan *load* *Flash Sale*.
*   **Workspace**: `go.work` (Go Workspaces) digunakan untuk mengatur struktur Monorepo dengan **6 module independen**: `api-gateway`, `auth-service`, `inventory-service`, `order-service`, `payment-service`, `product-service` + `shared` + `proto`.

## 2. Microservice Framework & RPC
*   **Framework**: `go-kratos/kratos/v2`
    *   **Alasan**: Framework berbobot ringan (*lightweight*) yang dirancang khusus untuk memfasilitasi integrasi HTTP REST dan gRPC secara mulus (*seamless*). Sangat cocok dipasangkan dengan pola *Hexagonal Architecture*. Kratos tidak memaksakan struktur folder, ia hanya menyediakan *toolkit* (Middleware, Transport, Config) yang elegan.
*   **Kontrak Komunikasi Internal**: `gRPC` & `Protobuf`
    *   **Alasan**: Komunikasi antar-*service* (seperti API Gateway memanggil Reserve Stock ke Inventory Service) dilakukan via gRPC agar sangat cepat (*binary serialized*) dan memiliki kontrak yang strongly-typed.

## 3. Database & Cache
*   **Database Relasional**: `PostgreSQL`
    *   **Alasan**: Tangguh, mendukung fitur transaksional (ACID) kuat, dan memiliki isolasi level yang baik untuk operasi kritikal (Saga Pattern, Outbox Pattern).
*   **Driver DB**: `jackc/pgx`
    *   **Alasan**: Driver standar *de facto* untuk Postgres di Go yang memiliki performa jauh lebih baik dari `lib/pq` lama.
*   **Query Builder / Mapper**: `jmoiron/sqlx`
    *   **Alasan**: Memungkinkan penulisan raw SQL (menjaga visibilitas performa index/query) sambil memberikan kenyamanan *struct scanning/mapping* tanpa *overhead* yang biasanya ditimbulkan oleh ORM penuh seperti GORM. Kecepatan query murni sangat ditekankan pada skenario *Flash Sale*.
*   **Caching & Atomic Operations**: `Redis`
    *   **Alasan**: Redis memegang peranan paling sentral di proyek *Flash Sale*. Digunakan untuk menyimpan stok awal. Redis Lua Script digunakan untuk mengunci kuota dan mengurangi stok secara *atomic* dan *thread-safe* menghindari *Race Condition* di konkurensi ekstrem.

## 4. Message Broker (Event-Driven Async)
*   **Broker**: `Apache Kafka`
    *   **Alasan**: Mampu memproses jutaan pesan per detik (*High Throughput*). Mendukung *log append-only* sehingga *event* (*Event Sourcing* / Saga) bersifat persisten dan tidak hilang meskipun *service* *consumer* mati.
*   **Go Client**: `twmb/franz-go`
    *   **Alasan**: Client Kafka untuk Go yang dikembangkan khusus untuk kecepatan maksimum, dengan API yang modern dan dukungan context secara *native*, mengalahkan performa `sarama` (Shopify) dan `confluent-kafka-go` (butuh CGO).

## 5. Observability (Log & Trace)
*   **Log**: `log/slog` (Go 1.21+ bawaan)
    *   **Alasan**: Standar logger terstruktur bawaan Go. Digunakan bersama JSON handler.
*   **Tracing**: `OpenTelemetry` (OTel)
    *   **Alasan**: Standar industri modern untuk *distributed tracing*. Jejak *request* dari API Gateway -> Kafka -> Order Service bisa dilacak secara visual menggunakan Jaeger di infrastruktur lokal.

## 6. Reverse Proxy & API Gateway
*   **Reverse Proxy**: `NGINX` (Ditaruh pada layer paling depan di Docker Compose). Mengurus *Rate Limiting* dan pemantauan trafik L7.
*   **API Gateway (BFF)**: Aplikasi Go murni yang bertugas mem-parsing Auth/JWT pengguna, mengagregasi data, dan menjadi tameng sebelum *request* dikirimkan secara internal lewat gRPC ke *backend services*.

## 7. Resilience (Ketahanan Sistem)
*   **Circuit Breaker**: `github.com/sony/gobreaker`
    *   **Alasan**: Ringan, murni Go (tanpa CGO), tidak ada dependensi eksternal, API sederhana dan idiomatis. Alternatif seperti `afex/hystrix-go` lebih kompleks dan kurang aktif. CB ditempatkan di API Gateway karena dialah yang memanggil semua service downstream.
*   **Retry Strategy**: Implementasi kustom murni Go (`shared/pkg/resilience/retry.go`)
    *   **Alasan**: Tidak perlu library eksternal untuk logika retry sederhana. Exponential backoff dengan jitter ±30% cukup untuk mencegah thundering herd saat recovery.
*   **Dead Letter Queue (DLQ)**: Kafka topic `flashsale.order.dlq` (untuk Order Service Consumer) dan `flashsale.inventory.dlq` (untuk Inventory Service Consumer)
    *   **Alasan**: Alternatif (drop atau retry tanpa batas) keduanya buruk untuk produksi. DLQ menjamin event tidak hilang sekaligus tidak memblokir consumer pipeline.

## 8. Optimasi Kinerja Tinggi (High-Performance Optimizations)

Untuk memastikan sistem mampu menangani beban ekstrem pada saat detik-detik pertama Flash Sale dimulai, beberapa teknik optimasi tingkat lanjut berikut telah diterapkan secara native di dalam kode:

1.  **Dynamic CPU Threading (`go.uber.org/automaxprocs`)**
    *   **Penerapan**: Diimpor sebagai blank import (`_ "go.uber.org/automaxprocs"`) di setiap berkas `main.go` layanan mikro.
    *   **Alasan**: Di lingkungan kontainer (seperti Docker/Kubernetes) atau cgroups VPS, Go secara default mendeteksi total CPU fisik *host*, bukan batas CPU kontainer. Hal ini menyebabkan jumlah thread sistem Go terlalu banyak dan memicu persaingan CPU yang hebat (*thread contention*). Library ini mengoreksi nilai `GOMAXPROCS` secara dinamis sesuai alokasi CPU yang didefinisikan kontainer.

2.  **Manajemen Pool Koneksi (Postgres & Redis Connection Pooling)**
    *   **PostgreSQL**: Dikonfigurasi secara eksplisit menggunakan limit pool koneksi (diverifikasi dari `shared/pkg/database/postgres.go`):
        *   `MaxOpenConns (25)`: Batas koneksi aktif per service. Dengan 5 service × 25 = 125 total — dalam batas aman `max_connections` PostgreSQL.
        *   `MaxIdleConns (10)`: Mempertahankan 10 koneksi idle yang siap digunakan kembali tanpa biaya jabat tangan TCP ulang.
        *   `ConnMaxLifetime (5 menit)`: Rotasi koneksi sebelum LB cloud (AWS/GCP, idle timeout ~4 menit) menutupnya secara paksa.
        *   `ConnMaxIdleTime (2 menit)`: Melepas koneksi idle yang sudah terlalu lama tidak digunakan.
    *   **Redis**: Dikonfigurasi dengan `PoolSize: 500` dan `MinIdleConns: 50` di *Inventory Service* untuk mendukung eksekusi cepat ribuan kueri Lua Script per detik tanpa hambatan pembuatan soket baru.

3.  **Kafka Network I/O Compression (Snappy)**
    *   **Penerapan**: Mengaktifkan kompresi Snappy pada `kgo.Client` di *Outbox Relay Worker* (`shared/pkg/outbox/relay.go`).
    *   **Alasan**: Kompresi Snappy memberikan keseimbangan luar biasa antara efisiensi CPU dan rasio kompresi. Hal ini mengurangi konsumsi *bandwidth* jaringan hingga 80% saat mengirimkan batch event bisnis berukuran besar ke Kafka, tanpa menurunkan *throughput* CPU.

4.  **Nginx High-Concurrency Connection Reuse (Keepalive)**
    *   **Penerapan**: Di dalam `nginx.conf`, dikonfigurasi pooling koneksi ke backend upstream:
        ```nginx
        upstream gateway {
            server host.docker.internal:18000;
            keepalive 100; # Pertahankan 100 koneksi persisten ke API Gateway
        }
        ```
    *   **Alasan**: Meng-upgrade protokol proxy ke HTTP 1.1 dan mempertahankan koneksi persisten (*keep-alive*) mencegah penumpukan soket berstatus `TIME_WAIT` (kehabisan port lokal / *TCP Port Exhaustion*) pada Nginx saat jutaan request masuk bersamaan.

