# Arsitektur Docker Compose (Local & Demo)

## 1. Tujuan

Dokumen ini menjelaskan topologi jaringan lokal (`docker-compose.yml`) untuk menjalankan seluruh sistem dalam satu perintah. Berguna untuk pengembangan lokal, demo, dan integrasi testing.

> [!NOTE]
> Semua detail port, service, dan konfigurasi dalam dokumen ini diverifikasi langsung dari `docker-compose.yml` dan `.env.example`.

**Desain utama:** Go Services dikemas dalam **Docker image** (via `Dockerfile` per service) dan dijalankan dengan `docker compose --profile app up`. Infrastruktur (DB, Cache, Broker, Observability) dan aplikasi bisa dijalankan secara terpisah.

---

## 2. Daftar Lengkap Service

Docker Compose lokal mendefinisikan **18 service** (12 Infrastruktur + 6 Aplikasi) dalam satu jaringan bridge `flashsale_net`. 

> 💡 **Learning Point:** Pemisahan antara infrastruktur dan aplikasi (melalui `profile`) memungkinkan kita menjalankan *database* dan *broker* saja (tanpa menjalankan aplikasinya) saat kita ingin menjalankan layanan Go secara mandiri (misal via `go run`) untuk proses *debugging*.

### Infrastruktur Observabilitas & Pendukung (tanpa profile)

| Container | Image | Port Host → Container | Fungsi Detail |
|---|---|---|---|
| `flashsale-postgres` | `postgres:15-alpine` | `15432 → 5432` | **RDBMS Utama**. Menggunakan 1 *instance* namun menampung 5 *logical database* terpisah. |
| `flashsale-redis` | `redis:7-alpine` | `16379 → 6379` | **Cache & In-Memory Store**. Digunakan untuk *atomic counter* Lua Script (stok) dan menyimpan *blacklist* JTI dari JWT. |
| `flashsale-kafka` | `bitnamilegacy/kafka:3.5.1` | `19092 → 9092`, `19094 → 9094` | **Message Broker (KRaft Mode)**. Jantung komunikasi asinkron antar layanan (Saga Pattern). |
| `flashsale-kafka-ui` | `provectuslabs/kafka-ui` | `18080 → 8080` | **Kafka Web UI**. Sangat berguna bagi developer untuk memantau pesan masuk (Event) di dalam topik. |
| `flashsale-otel-collector`| `otel/opentelemetry-collector` | `4317` (gRPC), `4318` (HTTP) | **OpenTelemetry Collector**. Mengumpulkan jejak (*trace*) dari layanan Go dan mendistribusikannya ke Jaeger & Prometheus. |
| `flashsale-jaeger` | `jaegertracing/all-in-one` | `16686 → 16686` | **Distributed Tracing**. Menampilkan perjalanan *request* antar microservice secara visual. |
| `flashsale-prometheus` | `prom/prometheus` | `9090 → 9090` | **Time-Series Database**. Menyimpan metrik sistem (seperti penggunaan CPU, jumlah *request* RPS). |
| `flashsale-grafana` | `grafana/grafana` | `3000 → 3000` | **Dashboard Visualisasi**. Menggabungkan data dari Prometheus (Metrik) dan Loki (Log) ke dalam satu UI. |
| `flashsale-loki` | `grafana/loki` | `3100 → 3100` | **Log Aggregator**. Menyimpan log tersentralisasi. |
| `flashsale-promtail` | `grafana/promtail` | - | **Log Scraper**. Mengumpulkan log dari file dan mengirimkannya ke Loki. |
| `flashsale-nginx` | `nginx:alpine` | `18081 → 80` | **Reverse Proxy & Rate Limiter**. Titik masuk utama untuk *client* mencegah DDoS. |
| `flashsale-swagger-ui` | `swaggerapi/swagger-ui` | `18082 → 8080` | **Dokumentasi API**. UI interaktif untuk menguji REST API Gateway secara visual. |

### Go Services (profile: `app`)

| Container | Port Host → Container | Fungsi |
|---|---|---|
| `flashsale-api-gateway` | `18000 → 8000` | BFF HTTP REST, auth JWT, dispatch ke gRPC services |
| `flashsale-product-service` | `19001 → 9001` | gRPC server — katalog produk |
| `flashsale-inventory-service` | `19002 → 9002` | gRPC server — stock management + Outbox Worker |
| `flashsale-payment-service` | `19003 → 9003` | gRPC server — payment processing |
| `flashsale-auth-service` | `19004 → 9004` | gRPC server — register/login JWT (RS256) |
| `flashsale-order-service` | *(tidak expose ke host)* | Kafka consumer only — Saga orchestration |

---

## 3. Topologi Jaringan & Observabilitas

```text
[Browser/Client]
      │ HTTP
      ▼
+--------------------------------------------+
| flashsale-nginx (host:18081 → 80)          |
| Rate Limiting: 10 req/s per IP, burst 20   |
| keepalive 100 koneksi ke API Gateway       |
+--------------------------------------------+
      │ HTTP/1.1 proxy
      ▼
+--------------------------------------------+
| flashsale-api-gateway (host:18000 → 8000)  |
| - Validasi JWT RS256 + JTI blacklist Redis  |
| - tracing.Server() OTel middleware          |
| - Circuit Breaker per downstream            |
+--------------------------------------------+
      │ gRPC (tracing.Client() inject traceparent)
      ├──── product-service:9001 (Product Catalog)
      ├──── inventory-service:9002 (Reserve Stock via Lua)
      ├──── payment-service:9003 (Process Payment)
      └──── auth-service:9004 (Register/Login JWT)

[Async Layer — Transactional Outbox Pattern]
inventory-service ──INSERT──→ db_inventory.outbox_messages
payment-service   ──INSERT──→ db_payment.outbox_messages
order-service     ──INSERT──→ db_order.outbox_messages
                               │
                    [Relay Worker — poll setiap 1 detik]
                    FOR UPDATE SKIP LOCKED + retry 5x
                               │ traceparent di Kafka Header
                               ▼
+--------------------------------------------+
| flashsale-kafka (host:19092, 19094)        |
| Topics:                                    |
|   flashsale.inventory.events  → order-svc  |
|   flashsale.payment.events    → order-svc  |
|   flashsale.order.events      → inv-svc    |
+--------------------------------------------+
      │ Kafka Consumer (franz-go, DisableAutoCommit)
      ├──── order-service: consume inventory.events + payment.events
      └──── inventory-service: consume order.events (OrderCancelledEvent)

[Observability Stack]
All Go services ──OTLP gRPC batch──→ otel-collector:4317
                                        ├──→ jaeger:16686 (Tracing UI)
                                        └──→ prometheus:9090 (Metrics)
                                                  │
Promtail ──Scrape Logs──→ loki:3100 ──────────────┴──→ grafana:3000 (Unified UI)
```

---

## 4. Inisialisasi Database

Saat container `postgres` pertama kali dijalankan, script `init-dbs.sh` di-mount ke `/docker-entrypoint-initdb.d/` dan dieksekusi otomatis. Script ini membuat **5 database terpisah**:

| Database | Digunakan oleh |
|---|---|
| `db_product` | product-service |
| `db_inventory` | inventory-service |
| `db_order` | order-service |
| `db_payment` | payment-service |
| `db_auth` | auth-service |

Master database `flashsale_master` adalah database default PostgreSQL yang dibuat saat startup (tidak digunakan oleh service manapun secara langsung).

---

## 5. Cara Menjalankan

### Hanya Infrastruktur (untuk development lokal)
```bash
# Copy .env
cp .env.example .env

# Jalankan semua infrastruktur (postgres, redis, kafka, jaeger, nginx)
docker compose up -d

# Cek status
docker compose ps
```

### Infrastruktur + Semua Go Services
```bash
# Jalankan semua (infra + services)
docker compose --profile app up -d

# Atau menggunakan Makefile (jika ada)
make up
```

### Menjalankan Hanya Satu Service
```bash
docker compose --profile app up inventory-service -d
```

---

## 6. Port Mapping Lengkap (Host)

| Service | Port Host | Akses Lokal |
|---|---|---|
| **API Gateway** (HTTP) | `18000` | `http://localhost:18000` |
| **Nginx** (Reverse Proxy) | `18081` | `http://localhost:18081` |
| **Swagger UI** (API Docs)| `18082` | `http://localhost:18082` |
| **Grafana UI** (Metrics)| `3000`  | `http://localhost:3000` |
| **Prometheus UI**       | `9090`  | `http://localhost:9090` |
| **PostgreSQL**          | `15432` | `localhost:15432` |
| **Redis**               | `16379` | `localhost:16379` |
| **Kafka** (internal)    | `19092` | `localhost:19092` |
| **Kafka** (external)    | `19094` | `localhost:19094` |
| **Kafka UI**            | `18080` | `http://localhost:18080` |
| **Jaeger UI**           | `16686` | `http://localhost:16686` |
| **Auth Service** (gRPC) | `19004` | `localhost:19004` |
| **Product Service** (gRPC)| `19001` | `localhost:19001` |
| **Inventory Service** (gRPC)| `19002` | `localhost:19002` |
| **Payment Service** (gRPC)| `19003` | `localhost:19003` |

> [!NOTE]
> Port host sengaja menggunakan angka non-standar (prefix `1xxxx`) untuk menghindari konflik dengan service yang mungkin sudah berjalan di mesin host. Semua port dapat dikonfigurasi melalui `.env`.

---

## 7. Konfigurasi Kafka (KRaft Mode)

Kafka berjalan dalam mode **KRaft** (tanpa Zookeeper) menggunakan image `bitnamilegacy/kafka:3.5.1`. Konfigurasi listener:

| Listener | Alamat | Digunakan oleh |
|---|---|---|
| `PLAINTEXT` | `kafka:9092` | Service dalam Docker network |
| `EXTERNAL` | `localhost:19094` | Service di host (development lokal) |
| `CONTROLLER` | `kafka:9093` | Internal Kafka controller |

Topic dibuat secara otomatis (`KAFKA_CFG_AUTO_CREATE_TOPICS_ENABLE=true`).

---

## 8. Kenapa Kita Punya Dua File? (Local vs Prod)

Sistem ini menyediakan dua spesifikasi:
1. `docker-compose.yml` (Development / Local)
2. `docker-compose.prod.yml` (Production HA / Spike Test)

**Jawaban Singkat:** Tidak perlu dua dokumen terpisah, karena arsitektur aplikasinya tetap sama persis. Yang berbeda hanyalah **Topologi Infrastrukturnya** (khususnya Redis dan Kafka) untuk menangani *High Availability* (ketersediaan tinggi).

### Penjelasan Detail Perbedaan (Local vs Prod):

| Komponen | `docker-compose.yml` (Lokal) | `docker-compose.prod.yml` (Produksi) |
|---|---|---|
| **Redis Setup** | **Standalone** (1 kontainer `redis`). Mudah dan ringan di RAM. | **Sentinel HA** (1 Master, 2 Replica, 3 Sentinel). Jika Master mati, Sentinel otomatis memilih Replica menjadi Master baru. |
| **Koneksi Redis di Go** | Menggunakan variabel `REDIS_ADDR=redis:6379` (koneksi langsung tunggal) | Menggunakan *Failover Client* dengan `REDIS_SENTINEL_ADDRS=...` dan `REDIS_MASTER_NAME=mymaster`. |
| **Kafka Partitions** | **Default** (Umumnya 1 partisi). Cukup untuk testing satu persatu. | Dikonfigurasi dengan **10 Partisi** (di *source code* admin). Memungkinkan 10 order-service memproses event secara paralel! |

> 💡 **Learning Point: Mengapa Redis Sentinel sangat penting di Produksi?**
> Dalam sistem *Flash Sale*, Redis adalah penjaga gawang (*gatekeeper*) stok via *Lua Script*. Jika 1 instance Redis mati (Single Point of Failure), seluruh penjualan macet seketika. Dengan menggunakan **Sentinel**, Redis akan memiliki cadangan yang otomatis menggantikan peran utama dalam hitungan 2-3 detik tanpa intervensi manusia, sehingga sistem tetap tahan banting saat diserbu ribuan pembeli secara serentak.

### Cara Menjalankan Lingkungan Prod (Simulasi)

```bash
# Menjalankan versi Prod (pastikan environment lokal dimatikan dulu)
make prod-up

# Test ketahanan dengan script K6
make test-spike-prod     # Test lonjakan drastis (Spike)
make test-breakpoint     # Mencari batas maksimal (Breakpoint)
```
