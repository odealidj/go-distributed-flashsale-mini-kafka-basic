# 📐 BLUEPRINT: Flash Sale Distributed System

> **Dokumen ini adalah spesifikasi teknologi-agnostik.** Dibaca oleh AI Engine atau engineer mana pun yang ingin **merekonstruksi sistem ini dari awal menggunakan stack teknologi yang berbeda**, tetapi dengan **hasil dan perilaku yang identik**.
>
> Referensi implementasi asli: Go + Kafka + Redis + PostgreSQL + gRPC + OpenTelemetry  
> Dokumentasi teknis tersimpan di direktori `docs/` pada repositori ini.

---

## 📋 DAFTAR ISI

1. [Konteks Bisnis & Tujuan Sistem](#1-konteks-bisnis--tujuan-sistem)
2. [Aturan Bisnis Wajib (Non-Negotiable)](#2-aturan-bisnis-wajib-non-negotiable)
3. [Arsitektur Tingkat Tinggi](#3-arsitektur-tingkat-tinggi)
4. [Daftar Microservice & Tanggung Jawabnya](#4-daftar-microservice--tanggung-jawabnya)
5. [Kontrak API Publik (HTTP)](#5-kontrak-api-publik-http)
6. [Kontrak Komunikasi Internal Antar-Service](#6-kontrak-komunikasi-internal-antar-service)
7. [Skema Database Per Service](#7-skema-database-per-service)
8. [Kontrak Event & Payload (Message Broker)](#8-kontrak-event--payload-message-broker)
9. [State Machine](#9-state-machine)
10. [Pola Arsitektur Wajib Diimplementasikan](#10-pola-arsitektur-wajib-diimplementasikan)
11. [Background Workers & Jobs](#11-background-workers--jobs)
12. [Pola Resilience Wajib](#12-pola-resilience-wajib)
13. [Observability](#13-observability)
14. [Panduan Penggantian Teknologi](#14-panduan-penggantian-teknologi)
15. [Urutan Implementasi yang Disarankan](#15-urutan-implementasi-yang-disarankan)
16. [Verifikasi Kebenaran Sistem](#16-verifikasi-kebenaran-sistem)

---

## 1. Konteks Bisnis & Tujuan Sistem

### 1.1 Apa yang Dibangun

Sebuah **backend Flash Sale** — sistem di mana produk berstock terbatas dijual dengan harga diskon besar dalam waktu singkat. Ribuan pengguna berebut membeli di saat bersamaan.

### 1.2 Dua Tantangan Inti

| # | Tantangan | Konsekuensi jika gagal |
|---|---|---|
| 1 | **Zero Oversell** — stok tidak boleh minus | Kerugian finansial, kepercayaan runtuh |
| 2 | **Low Latency** — response checkout ≤ 100ms | User pergi, sistem dianggap rusak |

### 1.3 User Journey (Happy Path)

```
1. User register → login → dapat token JWT
2. User lihat daftar produk Flash Sale
3. User POST /checkout → sistem potong stok → jawab langsung 202
4. (Background) sistem buat record order
5. User POST /pay → sistem proses bayar → jawab langsung
6. (Background) sistem update status order → PAID
7. User GET /orders/{id} → cek status = PAID
```

### 1.4 Sad Path (Kompensasi)

```
Jika user tidak bayar dalam 15 menit:
  → Order otomatis dibatalkan
  → Stok dikembalikan ke etalase
  → User lain bisa beli slot tersebut
```

---

## 2. Aturan Bisnis Wajib (Non-Negotiable)

Ini adalah aturan yang **tidak boleh dilanggar** oleh implementasi mana pun, apapun teknologinya:

| ID | Nama | Aturan |
|---|---|---|
| **BR-001** | No Oversell | `stok_terjual + stok_tersisa = stok_awal` SELALU. Stok tidak boleh < 0. |
| **BR-002** | 1 Item Per User | Satu user hanya bisa punya 1 slot pemesanan aktif per produk Flash Sale. |
| **BR-003** | Payment Timeout | Order `PENDING` yang tidak dibayar dalam **tepat 15 menit** harus `CANCELLED` dan stoknya dikembalikan. |
| **BR-004** | Idempotency Required | Setiap request checkout dan setiap pemrosesan event broker harus idempoten — operasi yang sama boleh datang >1x tapi hasilnya tetap sama. |

---

## 3. Arsitektur Tingkat Tinggi

### 3.1 Topology

```
[Client] 
  → [Rate Limiter / Reverse Proxy]  ← lapisan infrastruktur, bukan service
  → [API Gateway]                   ← satu-satunya pintu masuk ke sistem
  → [Auth Service]        (gRPC / internal sync)
  → [Product Service]     (gRPC / internal sync)
  → [Inventory Service]   (gRPC / internal sync) ← PALING KRITIS
  → [Order Service]       (gRPC / internal sync, BACA saja dari gateway)
  → [Payment Service]     (gRPC / internal sync)
  
[Inventory Service] → [Message Broker] → [Order Service]
[Order Service]     → [Message Broker] → [Inventory Service]
[Payment Service]   → [Message Broker] → [Order Service]
```

### 3.2 Prinsip Desain Wajib

| Prinsip | Aturan |
|---|---|
| **Database per Service** | Tidak ada service yang mengakses database milik service lain secara langsung. |
| **Komunikasi internal** | Service berbicara satu sama lain hanya via: RPC call (sinkron) atau Message Broker event (asinkron). |
| **Gateway sebagai satu-satunya pintu** | Client tidak boleh memanggil service langsung, selalu lewat API Gateway. |
| **Auth terdesentralisasi** | API Gateway memvalidasi token sendiri (punya kunci publik). Auth Service hanya dipanggil saat register/login. Ini mencegah Auth Service jadi bottleneck. |
| **Checkout adalah sinkron** | Operasi potong stok HARUS sinkron dan hasilnya dikembalikan ke user. Tidak boleh async. |
| **Order creation adalah asinkron** | Order dibuat setelah checkout sukses, digerakkan oleh event dari message broker. |

---

## 4. Daftar Microservice & Tanggung Jawabnya

### 4.1 API Gateway

- **Tujuan:** Pintu masuk tunggal. Memvalidasi JWT, mendelegasikan ke service yang tepat.
- **Protokol keluar ke client:** HTTP/REST
- **Protokol ke service:** RPC internal (gRPC / REST / tRPC — bebas pilih)
- **Tanggung jawab:**
  - Validasi JWT secara mandiri menggunakan kunci publik (tanpa memanggil Auth Service)
  - Ekstrak `userID` dari token untuk diteruskan ke service
  - Melakukan Rate Limiting atau mendelegasikannya ke layer di depannya
  - Menerapkan Circuit Breaker untuk setiap service downstream
  - Timeout tiap RPC call ke downstream (referensi: 3 detik)
  - Propagasi trace context ke semua downstream call

### 4.2 Auth Service

- **Tujuan:** Satu-satunya penerbit token di seluruh sistem.
- **Tanggung jawab:**
  - Register: terima `username` + `password` → hash password (bcrypt) → simpan ke DB
  - Login: verifikasi hash → terbitkan JWT signed dengan **kunci privat RSA** (algoritma RS256)
  - JWT mengandung: `sub` (userID), `jti` (token ID unik per token), `exp` (expiry 24 jam)
  - **Tidak** memvalidasi token — itu bukan tugasnya

### 4.3 Product Service

- **Tujuan:** Katalog produk yang tersedia di Flash Sale.
- **Tanggung jawab:**
  - Mengembalikan daftar produk dengan harga normal dan harga Flash Sale
  - Mendukung pagination (page + per_page / offset + limit)
- **Catatan:** Dalam implementasi ini, data produk adalah seed data statis. Tidak ada CRUD produk.

### 4.4 Inventory Service *(Paling Kritis)*

- **Tujuan:** Penjaga kebenaran stok. Mencegah oversell.
- **Tanggung jawab:**
  - `ReserveStock` (dipanggil oleh Gateway saat checkout):
    1. Cek idempotency key di cache (tolak jika duplikat)
    2. Cek stok tersedia di cache in-memory
    3. Potong stok secara **atomik** (tidak bisa diinterupsi request lain)
    4. Simpan idempotency key dengan TTL 2 jam
    5. Tulis event `StockReservedEvent` ke outbox (dalam 1 transaksi DB)
    6. Return sukses/gagal ke Gateway
  - Konsumsi event `OrderCancelledEvent`:
    1. Kembalikan stok secara atomik
    2. Hapus idempotency key
  - Menjalankan **Reconciliation Job** (background, tiap 1 menit)
  - Menjalankan **Relay Worker** (background, publish outbox ke broker)

> **KRITIKAL:** Operasi potong/kembalikan stok harus **atomik**. Artinya dalam satu operasi yang tidak bisa disela, cek + modifikasi harus terjadi bersamaan. Di implementasi asli: Redis Lua Script. Di implementasi lain: bisa database transaction dengan SELECT FOR UPDATE, atau CAS operation.

### 4.5 Order Service

- **Tujuan:** Pencatat transaksi pesanan, sepenuhnya event-driven.
- **Tanggung jawab:**
  - **Tidak pernah membuat order sendiri** — hanya bereaksi terhadap event
  - Konsumsi `StockReservedEvent` → buat order baru dengan status `PENDING`
  - Konsumsi `PaymentCompletedEvent` → ubah status order ke `PAID`
  - Konsumsi `PaymentFailedEvent` → ubah status ke `CANCELLED` → tulis `OrderCancelledEvent` ke outbox
  - Menjalankan **Timeout Worker** (background, tiap 30 detik) untuk mendeteksi order `PENDING` > 15 menit
  - `GetOrder` (dipanggil oleh Gateway saat client query status)
  - Menjalankan **Relay Worker** (background, publish outbox ke broker)

### 4.6 Payment Service

- **Tujuan:** Jembatan ke payment gateway eksternal.
- **Tanggung jawab:**
  - `ProcessPayment`: terima `orderID` + `amount` → simulasikan pembayaran → tulis hasil ke outbox
  - Aturan simulasi: `amount mod 10 == 4` → GAGAL, selainnya → SUKSES
  - Tulis event `PaymentCompletedEvent` atau `PaymentFailedEvent` ke outbox
  - Menjalankan **Relay Worker** (background, publish outbox ke broker)

---

## 5. Kontrak API Publik (HTTP)

> File lengkap: `docs/openapi.yaml`

| Method | Path | Auth Required | Deskripsi |
|---|---|---|---|
| `POST` | `/api/v1/register` | Tidak | Daftarkan user baru |
| `POST` | `/api/v1/login` | Tidak | Login, dapat JWT |
| `GET` | `/api/v1/products` | Ya (JWT) | Daftar produk Flash Sale |
| `POST` | `/api/v1/checkout` | Ya (JWT) | Potong stok, mulai proses beli |
| `POST` | `/api/v1/pay` | Ya (JWT) | Bayar pesanan |
| `GET` | `/api/v1/orders/{order_id}` | Ya (JWT) | Cek status pesanan |

### 5.1 Request & Response Detail

**`POST /api/v1/register`**
```json
Request: { "username": "string", "password": "string" }
Response 201: { "id": "uuid", "username": "string" }
Response 409: { "error": "username sudah terdaftar" }
```

**`POST /api/v1/login`**
```json
Request: { "username": "string", "password": "string" }
Response 200: { "access_token": "eyJ...", "token_type": "Bearer" }
Response 401: { "error": "invalid credentials" }
```

**`GET /api/v1/products`**
```json
Response 200: {
  "products": [
    { "id": "prod_1", "name": "string", "original_price": 500000, "flash_sale_price": 150000 }
  ]
}
```

**`POST /api/v1/checkout`**
```
Headers: Authorization: Bearer <token>
         X-Idempotency-Key: <uuid> ← WAJIB
Request: { "product_id": "prod_1", "quantity": 1 }
Response 202: { "order_id": "uuid", "status": "PENDING", "message": "checkout berhasil, menunggu pembayaran" }
Response 409: { "error": "stok habis" atau "request duplikat" }
Response 429: { "error": "rate limited" }
```

> **PENTING:** Response 202 berarti stok berhasil dipotong. Order record mungkin belum terbentuk karena dibuat secara asinkron. Client harus polling `/orders/{order_id}`.

**`POST /api/v1/pay`**
```
Headers: Authorization: Bearer <token>
Request: { "order_id": "uuid", "amount": 150000 }
Response 200: { "payment_id": "uuid", "status": "SUCCESS" atau "FAILED" }
```

**`GET /api/v1/orders/{order_id}`**
```json
Response 200: {
  "id": "uuid",
  "user_id": "uuid",
  "product_id": "prod_1",
  "quantity": 1,
  "total_amount": 150000,
  "status": "PENDING | PAID | CANCELLED",
  "created_at": "ISO8601",
  "updated_at": "ISO8601"
}
```

### 5.2 Perilaku Rate Limiter

- Layer: Reverse Proxy (Nginx, Envoy, Kong, atau setara)
- Limit: **10 req/detik per IP**, burst toleransi **20 req**
- Perilaku saat melewati: langsung `429 Too Many Requests` (tanpa antre)

---

## 6. Kontrak Komunikasi Internal Antar-Service

Implementasi asli menggunakan **gRPC + Protocol Buffers**. Bisa diganti dengan REST, tRPC, atau mekanisme RPC lain — yang penting kontrak request/response-nya sama.

> File proto tersimpan di `proto/` dalam repositori.

### 6.1 Auth Service

| RPC | Request Fields | Response Fields |
|---|---|---|
| `Register` | `username: string`, `password: string` | `id: string`, `username: string` |
| `Login` | `username: string`, `password: string` | `access_token: string` |

### 6.2 Product Service

| RPC | Request Fields | Response Fields |
|---|---|---|
| `ListFlashSaleProducts` | `page: int`, `per_page: int` | `products: []Product` (id, name, original_price, flash_sale_price) |

### 6.3 Inventory Service

| RPC | Request Fields | Response Fields |
|---|---|---|
| `ReserveStock` | `product_id: string`, `user_id: string`, `event_id: string (UUID)`, `quantity: int` | `success: bool` |

### 6.4 Order Service

| RPC | Request Fields | Response Fields |
|---|---|---|
| `GetOrder` | `order_id: string` | `Order` (id, user_id, product_id, quantity, total_amount, status, created_at, updated_at) |

### 6.5 Payment Service

| RPC | Request Fields | Response Fields |
|---|---|---|
| `ProcessPayment` | `order_id: string`, `amount: int64` | `payment_id: string`, `status: string` |

---

## 7. Skema Database Per Service

> Semua service menggunakan **relational database** (referensi: PostgreSQL). Bisa diganti dengan MySQL, CockroachDB, atau setara dengan SQL support. NoSQL tidak direkomendasikan karena membutuhkan ACID transaction.

### 7.1 db_auth — Auth Service

```sql
CREATE TABLE users (
    id            VARCHAR(50)  PRIMARY KEY,    -- UUID v4
    username      VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,        -- bcrypt hash, BUKAN plaintext
    created_at    TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP  DEFAULT CURRENT_TIMESTAMP
);
```

### 7.2 db_product — Product Service

```sql
CREATE TABLE products (
    id               VARCHAR(50)  PRIMARY KEY, -- format: "prod_1"
    name             VARCHAR(255) NOT NULL,
    original_price   BIGINT       NOT NULL,    -- dalam satuan terkecil (cent/rupiah)
    flash_sale_price BIGINT       NOT NULL,
    created_at       TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    updated_by       VARCHAR(100),
    version          INTEGER      DEFAULT 1    -- optimistic locking
);

-- Seed data wajib ada:
INSERT INTO products VALUES ('prod_1', 'Sepatu Lari X', 500000, 150000, NOW(), NOW(), 'system', 1);
INSERT INTO products VALUES ('prod_2', 'Tas Ransel Y',  300000,  99000, NOW(), NOW(), 'system', 1);
```

### 7.3 db_inventory — Inventory Service

```sql
CREATE TABLE inventories (
    product_id VARCHAR(50) PRIMARY KEY, -- referensi logis ke products.id
    stock      BIGINT      NOT NULL,    -- jumlah stok awal/saat ini di DB
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_by VARCHAR(100),
    version    INTEGER     DEFAULT 1
);

-- Seed data wajib ada:
INSERT INTO inventories VALUES ('prod_1', 100, NOW(), 'system', 1);
INSERT INTO inventories VALUES ('prod_2',  50, NOW(), 'system', 1);

-- Pola Outbox (struktur IDENTIK di semua service yang punya):
CREATE TABLE outbox_messages (
    id             SERIAL        PRIMARY KEY,
    aggregate_id   VARCHAR(255)  NOT NULL,  -- ID entitas (misal: event_id)
    aggregate_type VARCHAR(255)  NOT NULL,  -- nama domain ("inventory")
    event_type     VARCHAR(255)  NOT NULL,  -- nama event ("StockReservedEvent")
    payload        JSON         NOT NULL,  -- isi event dalam JSON
    trace_payload  VARCHAR(512),            -- trace context (opsional, untuk distributed tracing)
    status         VARCHAR(50)   NOT NULL DEFAULT 'PENDING', -- PENDING | SENT | FAILED
    created_at     TIMESTAMP     DEFAULT CURRENT_TIMESTAMP
);
```

### 7.4 db_order — Order Service

```sql
CREATE TABLE orders (
    id           VARCHAR(50) PRIMARY KEY,  -- UUID v4 = idempotency_key dari StockReservedEvent
    user_id      VARCHAR(50) NOT NULL,     -- dari JWT claim 'sub'
    product_id   VARCHAR(50) NOT NULL,
    quantity     INTEGER     NOT NULL,
    total_amount BIGINT      NOT NULL,     -- quantity × flash_sale_price
    status       VARCHAR(50) NOT NULL DEFAULT 'PENDING', -- PENDING | PAID | CANCELLED
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Identik dengan inventory, digunakan untuk OrderCancelledEvent:
CREATE TABLE outbox_messages ( /* sama dengan di atas */ );

-- Idempotency guard untuk consumer Kafka:
CREATE TABLE processed_events (
    event_id     VARCHAR(255) PRIMARY KEY, -- UUID dari payload event Kafka
    processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### 7.5 db_payment — Payment Service

```sql
CREATE TABLE payments (
    id         VARCHAR(50) PRIMARY KEY,  -- UUID v4
    order_id   VARCHAR(50) NOT NULL,     -- referensi logis ke orders.id
    amount     BIGINT      NOT NULL,
    status     VARCHAR(50) NOT NULL DEFAULT 'SUCCESS', -- SUCCESS | FAILED
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE outbox_messages ( /* sama dengan di atas */ );
CREATE TABLE processed_events ( /* sama dengan di atas */ );
```

### 7.6 Cache / In-Memory Store (Redis atau setara)

> Referensi: Redis. Bisa diganti dengan Memcached, Dragonfly, atau in-process cache dengan operasi atomik — tapi pilihan ini memiliki trade-off (lihat Bagian 14).

**Key patterns wajib:**

| Key Pattern | Tipe | TTL | Isi | Tujuan |
|---|---|---|---|---|
| `stock:{productID}` | Integer | Tidak ada | Jumlah stok tersedia | **Source of Truth stok saat Flash Sale** |
| `reserve_idemp:{eventID}` | String | **7200 detik (2 jam)** | `"productID:quantity"` | Idempotency guard + metadata untuk Reconciliation Job |
| `blacklist:{jti}` | String | Sama dengan JWT exp | `"1"` | Revoke JWT token |
| `product:list` | String (JSON) | 60 detik | JSON daftar produk | Cache produk agar tidak selalu query DB |

**Operasi atomik wajib (pada implementasi asli: Redis Lua Script, dapat diganti dengan mekanisme atomic scripting/transaction lain):**

```
ReserveStock(eventID, productID, quantity) → returns 0 atau 1:
  1. IF EXISTS reserve_idemp:{eventID} → return 0  (duplikat)
  2. stock = GET stock:{productID}
  3. IF stock < quantity → return 0  (stok tidak cukup)
  4. DECRBY stock:{productID} quantity
  5. SET reserve_idemp:{eventID} "{productID}:{quantity}" EX 7200
  6. return 1

RefundStock(eventID, productID, quantity):
  1. INCRBY stock:{productID} quantity
  2. DEL reserve_idemp:{eventID}
```

> **KRITIS:** Keenam langkah di `ReserveStock` harus berjalan sebagai **satu unit atomik**. Jika di tengah eksekusi ada request lain yang masuk, request lain itu harus menunggu atau dieksekusi setelahnya — tidak boleh interleaving. Ini yang mencegah race condition oversell.

---

## 8. Kontrak Event & Payload (Message Broker)

> Referensi: Apache Kafka. Bisa diganti dengan RabbitMQ, NATS, SQS, Pub/Sub (lihat trade-off di Bagian 14).  
> Semua payload event di-serialize sebagai **JSON**.

### 8.1 Topics / Queues

| Topic / Queue Name | Producer | Consumer |
|---|---|---|
| `flashsale.inventory.events` | Inventory Service | Order Service |
| `flashsale.order.events` | Order Service | Inventory Service |
| `flashsale.payment.events` | Payment Service | Order Service |
| `flashsale.inventory.dlq` | Order/Inventory Consumer (saat error permanen) | Tim Ops / Manual |
| `flashsale.order.dlq` | Order Consumer | Tim Ops / Manual |

### 8.2 StockReservedEvent

**Topic:** `flashsale.inventory.events`  
**Dikirim oleh:** Inventory Service (via Relay Worker dari outbox)  
**Dikonsumsi oleh:** Order Service

```json
{
  "event_id": "uuid-v4",           // ID unik event — dipakai sebagai idempotency key di processed_events
  "idempotency_key": "uuid-v4",    // dipakai sebagai order.id agar client bisa langsung tahu order ID-nya
  "user_id": "uuid-v4",
  "product_id": "prod_1",
  "quantity": 1
}
```

**Aksi consumer (Order Service):**
1. Cek `processed_events` WHERE `event_id = payload.event_id` → jika ada, skip (sudah diproses)
2. Dalam satu DB transaction: INSERT order + INSERT processed_events

### 8.3 PaymentCompletedEvent

**Topic:** `flashsale.payment.events`  
**Dikirim oleh:** Payment Service  
**Dikonsumsi oleh:** Order Service

```json
{
  "event_id": "uuid-v4",
  "order_id": "uuid-v4",
  "amount": 150000,
  "reason": ""          // KOSONG = PaymentCompleted. Ini yang membedakannya dari PaymentFailed.
}
```

### 8.4 PaymentFailedEvent

**Topic:** `flashsale.payment.events` *(topic yang sama!)*  
**Dikirim oleh:** Payment Service  
**Dikonsumsi oleh:** Order Service

```json
{
  "event_id": "uuid-v4",
  "order_id": "uuid-v4",
  "amount": 150000,
  "reason": "payment rejected by bank" // TIDAK KOSONG = PaymentFailed
}
```

> **Cara consumer membedakan dua event di topic yang sama:** Baca field `reason`. Jika `reason == ""` → PaymentCompleted. Jika `reason != ""` → PaymentFailed. Ini adalah keputusan desain yang bisa diubah (misal: pakai field `event_type` terpisah), tapi hasilnya harus sama.

### 8.5 OrderCancelledEvent

**Topic:** `flashsale.order.events`  
**Dikirim oleh:** Order Service  
**Dikonsumsi oleh:** Inventory Service

```json
{
  "event_id": "uuid-v4",
  "order_id": "uuid-v4",
  "product_id": "prod_1",
  "quantity": 1,
  "reason": "payment_timeout"  // atau "payment_failed" — tidak kosong
}
```

**Aksi consumer (Inventory Service):**
1. Jalankan operasi atomik `RefundStock(eventID, productID, quantity)`

### 8.6 Konfigurasi Message Broker yang Direkomendasikan

| Parameter | Nilai (referensi Kafka) | Alasan |
|---|---|---|
| Partisi per topic | 10 | Memungkinkan 10 consumer paralel saat scale-out |
| Replikasi faktor | 1 (dev) / 3 (prod) | Toleransi kegagalan 1 broker di prod |
| Delivery guarantee | At-Least-Once | Event mungkin duplikat, ditangani oleh idempotency |
| Acknowledgment | All ISR Replicas (producer) | Memastikan message tidak hilang meski satu broker mati |

---

## 9. State Machine

### 9.1 Order Status

```
                    PaymentCompletedEvent
    PENDING ─────────────────────────────────→ PAID
       │                                        (terminal)
       │  PaymentFailedEvent
       │  ATAU timeout > 15 menit
       └──────────────────────────────────────→ CANCELLED
                                                (terminal)
```

> **PENTING:** Transisi hanya boleh terjadi satu arah. Setelah `PAID` atau `CANCELLED`, status tidak boleh berubah lagi. Implementasi harus memastikan ini dengan kondisi `WHERE status = 'PENDING'` di query UPDATE.

### 9.2 Inventory Item (Logis)

```
AVAILABLE ──(ReserveStockCommand)──→ RESERVED ──(PaymentCompletedEvent)──→ DEDUCTED
                                        │                                    (terminal)
                                        └──(OrderCancelledEvent)──→ AVAILABLE
                                                                    (kembali ke awal)
```

### 9.3 Outbox Message Status

```
PENDING ──(Relay Worker berhasil publish ke broker)──→ SENT
   │                                                   (terminal)
   └──(Relay Worker gagal setelah N retry)──────────→ FAILED
                                                       (terminal, perlu intervensi manual)
```

---

## 10. Pola Arsitektur Wajib Diimplementasikan

### 10.1 Transactional Outbox Pattern

**Masalah yang diselesaikan:** Dual-write problem — bagaimana memastikan jika data bisnis tersimpan di DB, event ke broker *pasti* ikut terkirim?

**Cara kerja:**
```
SALAH (dual-write naif):
  1. Save ke DB ✅
  2. Publish ke broker ❌ (broker down?) → event hilang selamanya

BENAR (Outbox Pattern):
  1. BEGIN TRANSACTION
     Save data bisnis ke tabel domain
     INSERT event ke tabel outbox_messages (status=PENDING)
     COMMIT ← atomik, keduanya berhasil atau keduanya gagal
  2. Relay Worker (background thread / daemon task):
     SELECT ... FROM outbox_messages WHERE status='PENDING'
     Publish ke broker
     UPDATE outbox_messages SET status='SENT'
```

**Aturan implementasi:**
- Tabel `outbox_messages` ada di setiap service yang perlu publish event
- INSERT outbox selalu dalam transaksi DB yang sama dengan data bisnis
- Relay Worker poll secara periodik (referensi: setiap 1 detik)
- Relay Worker harus aman untuk multi-instance (gunakan `SELECT FOR UPDATE SKIP LOCKED` atau setara)

### 10.2 Saga Choreography (bukan Orchestration)

**Tanpa central coordinator.** Setiap service bereaksi terhadap event yang ia terima:

```
Inventory  →  [StockReservedEvent]  →  Order  →  (menunggu payment)
Payment    →  [PaymentCompletedEvent/FailedEvent]  →  Order
Order      →  [OrderCancelledEvent]  →  Inventory (refund stok)
```

**Tidak ada** service orchestrator yang tahu seluruh alur. Tidak ada step function, tidak ada workflow engine.

### 10.3 Two-Layer Idempotency

**Layer 1 — Cache (sebelum operasi):**
- Cek `reserve_idemp:{eventID}` di Redis sebelum memotong stok
- Jika sudah ada → tolak (duplikat), return gagal

**Layer 2 — Database (di consumer):**
- Cek `processed_events` tabel sebelum membuat/mengubah order
- Jika `event_id` sudah ada → skip, langsung return sukses

**Mengapa dua layer?**
- Layer 1 melindungi operasi atomik di cache (sangat cepat, O(1))
- Layer 2 melindungi pembuatan record di DB (persisten, tidak hilang setelah restart)

### 10.4 Desentralisasi JWT Validation

- **Auth Service** punya **kunci privat RSA** → sign JWT
- **API Gateway** punya **kunci publik RSA** → verifikasi JWT secara mandiri
- API Gateway **tidak memanggil** Auth Service untuk validasi

**Ini berarti:**
- Auth Service hanya dipanggil saat `/register` dan `/login`
- Validasi berlangsung sangat cepat (komputasi lokal, tanpa network call)
- Auth Service bisa down → semua endpoint yang butuh auth tetap berfungsi

---

## 11. Background Workers & Jobs

### 11.1 Relay Worker (ada di: Inventory, Order, Payment Service)

- **Interval:** Tiap 1 detik
- **Batch size:** 50 pesan per siklus
- **Query:**
  ```sql
  SELECT id, aggregate_type, event_type, payload, trace_payload
  FROM outbox_messages
  WHERE status = 'PENDING'
  ORDER BY created_at ASC
  LIMIT 50
  FOR UPDATE SKIP LOCKED; -- Atau mekanisme row-level lock non-blocking setara
  ```
- **Aksi:** Publish ke broker → UPDATE status='SENT'. Jika gagal setelah 5 retry → UPDATE status='FAILED'.
- **KRITIS:** `FOR UPDATE SKIP LOCKED` memungkinkan beberapa instance service berjalan paralel tanpa race condition.

### 11.2 Timeout Worker (ada di: Order Service)

- **Interval:** Tiap 30 detik
- **Query:**
  ```sql
  SELECT id, product_id, quantity
  FROM orders
  WHERE status = 'PENDING'
    AND created_at < NOW() - INTERVAL '15 minutes'
  FOR UPDATE SKIP LOCKED; -- Atau mekanisme row-level lock non-blocking setara
  ```
- **Aksi (dalam 1 DB transaction):**
  ```sql
  UPDATE orders SET status='CANCELLED', updated_at=NOW() WHERE id=$1;
  INSERT INTO outbox_messages (...OrderCancelledEvent...);
  ```

### 11.3 Reconciliation Job (ada di: Inventory Service)

- **Interval:** Tiap 1 menit
- **Grace Period:** 5 menit
- **Tujuan:** Mendeteksi "stock leak" — kondisi di mana stok terpotong di cache tapi event tidak masuk ke outbox (terjadi saat crash di antara dua operasi).
- **Cara kerja:**
  1. Scan semua `reserve_idemp:*` keys di cache yang berumur > 5 menit
  2. Untuk tiap key, cek apakah ada entri di `outbox_messages` WHERE `aggregate_id = eventID`
  3. Jika tidak ada → ini adalah kebocoran → jalankan RefundStock atomik
- **Metadata yang tersimpan di value cache:** `"{productID}:{quantity}"` → digunakan untuk refund

---

## 12. Pola Resilience Wajib

### 12.1 Circuit Breaker (di API Gateway)

- Satu Circuit Breaker per downstream service (bukan satu untuk semua)
- State: CLOSED → OPEN (saat 50% error dari min 10 request) → HALF-OPEN (setelah 5 detik)
- Saat OPEN: langsung return error ke client tanpa menunggu timeout

### 12.2 Timeout Per RPC Call

- Timeout 3 detik untuk setiap call dari API Gateway ke service manapun
- Jika timeout: return error ke client (bukan menunggu tanpa batas)

### 12.3 Retry + Exponential Backoff + Jitter

| Konteks | Max Retry | Delay Awal | Delay Max |
|---|---|---|---|
| Relay Worker publish ke broker | 5x | 200ms | 10s |
| Consumer process event | 3x | 500ms | 5s |

- **Jitter ±30%** wajib ditambahkan untuk mencegah thundering herd saat banyak worker retry bersamaan.
- Setelah semua retry habis → kirim ke DLQ (jangan drop event).

### 12.4 Dead Letter Queue

- Event yang gagal diproses setelah semua retry → kirim ke DLQ topic
- DLQ record menyertakan metadata: original topic, error message, timestamp

### 12.5 Rate Limiting

- Posisi: Di layer reverse proxy / API Gateway
- Konfigurasi: 10 req/detik per IP, burst 20, langsung tolak jika melewati

### 12.6 Database Connection Pool

- Max active connections: 25 per service
- Max idle connections: 10
- Connection max lifetime: 5 menit (untuk menghindari koneksi zombie dari LB timeout)

---

## 13. Observability

### 13.1 Distributed Tracing

- Setiap request dari client menghasilkan satu `trace_id` unik
- Trace context (`traceparent` header format W3C) harus **dipropagasikan** ke:
  - HTTP headers (API Gateway → client response)
  - RPC metadata/headers (API Gateway → downstream services)
  - Message broker record headers (producer → consumer)
- Consumer harus **mengekstrak** trace context dari message headers dan membuat child span
- Tujuan: satu trace bisa menampilkan perjalanan lengkap dari API Gateway → Kafka → Order Service

### 13.2 Health Checks

- Setiap service harus punya endpoint `/health` atau `/readiness`
- Reverse proxy menggunakan ini untuk mendeteksi service yang tidak sehat

---

## 14. Panduan Penggantian Teknologi

Panduan ini menjelaskan **apa yang boleh diganti, apa yang tidak boleh**, dan **apa yang perlu diperhatikan** saat mengganti.

### 14.1 Komponen yang Boleh Diganti Bebas

| Komponen Asli | Alternatif yang Setara | Catatan |
|---|---|---|
| Go | Java (Spring Boot), Python (FastAPI), Node.js (NestJS), Rust, Kotlin | — |
| gRPC + Protobuf | REST/JSON, tRPC, GraphQL | Kontrak request/response harus identik |
| PostgreSQL | MySQL, CockroachDB, Aurora | Wajib support ACID transaction dan `FOR UPDATE SKIP LOCKED` atau setara |
| Docker Compose | Kubernetes, Nomad, Systemd | — |
| Nginx | Envoy, Kong, Traefik, AWS ALB | Rate limiting harus tetap ada |
| Jaeger | Zipkin, Honeycomb, Datadog Tracing | Harus support OpenTelemetry protocol |

### 14.2 Komponen dengan Trade-Off Signifikan

**Apache Kafka → RabbitMQ:**
- ✅ Cocok untuk at-least-once delivery dan DLQ
- ⚠️ Tidak ada consumer group yang bisa read dari offset yang sama (replay sulit)
- ⚠️ Ordering per-partition tidak ada (tapi di sistem ini ordering tidak kritis)
- ✅ Lebih mudah dioperasikan

**Apache Kafka → NATS (JetStream):**
- ✅ Sangat ringan, latensi sangat rendah
- ✅ Cocok untuk skenario ini (di-design untuk exactly/at-least-once)
- ⚠️ Ekosistem tools lebih kecil (monitoring, replay tooling)

**Apache Kafka → Cloud Pub/Sub (SQS, Google Pub/Sub):**
- ✅ Zero operational overhead
- ⚠️ Ordering tidak dijamin per-message (bukan per-partition)
- ⚠️ Replay message lebih sulit

**Redis → PostgreSQL untuk Atomic Stock:**
- ⚠️ Bisa dilakukan dengan `SELECT FOR UPDATE` di PostgreSQL
- ⚠️ Akan lebih lambat (disk I/O vs in-memory), lebih banyak lock contention
- ✅ Menghilangkan dependency pada Redis
- **Cara implementasi:** `SELECT stock FROM inventories WHERE product_id=$1 FOR UPDATE` → validasi → `UPDATE inventories SET stock=stock-$qty WHERE product_id=$1 AND stock >= $qty`

**Redis → Hazelcast / Apache Ignite:**
- ✅ Mendukung distributed atomic operation
- ⚠️ Lebih kompleks di setup dan operasional

### 14.3 Komponen yang TIDAK Boleh Diganti Konsepnya

| Konsep | Alasan Tidak Boleh Dihilangkan |
|---|---|
| **Atomic stock operation** | Tanpa ini, oversell tidak bisa dicegah di bawah concurrency tinggi |
| **Transactional Outbox Pattern** | Tanpa ini, event bisa hilang saat service crash (dual-write problem) |
| **Idempotency Guard (dua layer)** | At-least-once delivery dari broker menyebabkan duplikasi — tanpa guard ini, order duplikat akan terbentuk |
| **Reconciliation Job** | Tanpa ini, "stock leak" tidak akan pernah terdeteksi (crash antara Redis dan Postgres write) |
| **Timeout Worker** | Tanpa ini, order PENDING yang tidak dibayar tidak akan pernah dibatalkan, stok tidak pernah kembali |
| **Circuit Breaker** | Tanpa ini, satu service down bisa cascading failure ke seluruh sistem |
| **Manual commit pada consumer** | Auto-commit dapat menyebabkan event hilang saat consumer crash setelah commit tapi sebelum proses selesai |

---

## 15. Urutan Implementasi yang Disarankan

Ikuti urutan ini untuk meminimalkan dependensi yang belum tersedia:

```
FASE 1: Foundation
  1. Setup infrastruktur: DB, Cache, Message Broker (Docker Compose)
  2. Auth Service (paling independen, tidak bergantung service lain)
  3. Product Service (tidak bergantung service lain)

FASE 2: Core Domain
  4. Inventory Service (bergantung: Cache + DB saja, belum bergantung service lain)
     → ReserveStock logic (atomic cache operation)
     → Outbox table + Relay Worker
     → ⚠️ JANGAN implementasi consumer dulu

FASE 3: Event Flow
  5. Order Service (bergantung: Inventory events)
     → Consumer StockReservedEvent
     → Create order logic
     → Outbox + Relay Worker untuk OrderCancelledEvent
     → Timeout Worker

FASE 4: Payment
  6. Payment Service
     → ProcessPayment logic
     → Outbox + Relay Worker untuk PaymentCompleted/FailedEvent
  7. Order Service: tambahkan consumer untuk payment events

FASE 5: Compensation & Self-Healing
  8. Inventory Service: tambahkan consumer untuk OrderCancelledEvent
  9. Inventory Service: Reconciliation Job

FASE 6: Edge & Infra
  10. API Gateway (bergantung semua service sudah berjalan)
      → JWT validation
      → Route ke semua service
      → Circuit Breaker
  11. Rate Limiter (Nginx/proxy layer)
  12. Distributed Tracing (tambahkan ke semua service)

FASE 7: Validasi
  13. Unit test: concurrent checkout (150 goroutine)
  14. Load test: 5000 virtual users
  15. Verifikasi: stok_terjual + stok_tersisa = stok_awal
```

---

## 16. Verifikasi Kebenaran Sistem

Setelah implementasi, sistem dianggap **benar** jika memenuhi semua kriteria berikut:

### 16.1 Test Zero Oversell

```
1. Set stok awal produk A = 100
2. Jalankan 500 concurrent checkout request untuk produk A
3. Setelah semua selesai, hitung:
   - total_sukses = jumlah checkout yang dapat response 202
   - stok_tersisa = GET stock:{prodA} dari cache
4. Assert: total_sukses + stok_tersisa == 100
5. Assert: stok_tersisa >= 0 (tidak pernah negatif)
```

### 16.2 Test Idempotency

```
1. Kirim checkout request dengan X-Idempotency-Key yang sama, 3 kali
2. Assert: hanya 1 yang sukses (202), sisanya 409
3. Assert: stok hanya terpotong 1 kali
```

### 16.3 Test Payment Timeout

```
1. Checkout → dapat order_id
2. Tunggu > 15 menit (atau mock waktu)
3. Assert: GET /orders/{order_id} → status == CANCELLED
4. Assert: stok dikembalikan (stok Redis bertambah 1)
```

### 16.4 Test Payment Failure Compensation

```
1. Checkout → dapat order_id
2. POST /pay dengan amount yang berakhiran 4 (contoh: 150004)
3. Tunggu beberapa detik (proses async)
4. Assert: GET /orders/{order_id} → status == CANCELLED
5. Assert: stok dikembalikan
```

### 16.5 Load Test (Referensi Hasil Implementasi Go)

```
Tool: k6 (atau Artillery, Gatling, Locust)
Konfigurasi: 5000 Virtual Users, durasi 30 detik, target endpoint /checkout
Target performa:
  - p95 latency < 500ms
  - Error rate < 1% (excluding 409 stok habis)
  - Zero 5xx errors
Verifikasi pasca test:
  - Jumlah order PAID/PENDING + stok tersisa = stok awal

Referensi batas maksimal arsitektur ini (sebagai acuan skalabilitas di lingkungan development menengah):
  - Mampu menahan lonjakan hingga 3.000 Request Per Detik (RPS)
  - 0.00% Error 500/Timeout
  - P95 Latency < 400ms pada beban puncak 3000 RPS
```

---

## 📌 Referensi Dokumen Teknis di Repositori Ini

Saat AI Engine atau engineer membaca dokumen ini dan ingin detail lebih dalam, bacalah dokumen-dokumen berikut:

| Dokumen | Lokasi | Isi |
|---|---|---|
| System Architecture | `docs/architecture/system-architecture.md` | Diagram dan penjelasan semua komponen |
| Domain Architecture | `docs/architecture/domain-architecture.md` | Diagram detail per domain service |
| Checkout Saga Flow | `docs/architecture/checkout-saga.md` | Alur lengkap checkout + payment + kompensasi |
| Background Workers | `docs/architecture/background-workers.md` | Detail pekerjaan Relay, Timeout, dan Reconciliation job |
| Resilience Patterns | `docs/architecture/resilience-patterns.md` | Detail semua pola ketahanan sistem |
| Docker Compose Architecture | `docs/deployment/docker-compose-architecture.md` | Topologi container lokal vs produksi |
| Docker Compose Deep Dive | `docs/deployment/docker-compose-deep-dive.md` | Panduan konfigurasi infrastruktur Docker baris-per-baris |
| Logical Data Model | `docs/database/logical-data-model.md` | Semua skema tabel + DML per endpoint |
| Event Contracts | `docs/events/event-contracts.md` | Payload event dan cara consumer routing |
| Kafka Design | `docs/events/kafka-operational-design.md` | Konfigurasi dan operasional Kafka |
| gRPC Contracts | `docs/grpc/grpc-contracts.md` | Spesifikasi lengkap protobuf API antar service |
| Hexagonal Architecture | `docs/implementation/go-hexagonal-architecture.md` | Struktur clean architecture (Ports and Adapters) |
| Tech Decisions | `docs/implementation/technology-decisions.md` | Penjelasan mengapa teknologi tertentu dipilih |
| Business Rules | `docs/specs/business-rules.yaml` | Aturan bisnis dalam format machine-readable |
| State Machines | `docs/specs/state-machines.yaml` | FSM order dan inventory |
| OpenAPI Spec | `docs/openapi.yaml` | Kontrak HTTP API lengkap |
| Interview Q&A | `docs/interview-qa.md` | Pertanyaan teknis + jawaban mendalam |

---

*Blueprint ini dibuat dari implementasi aktual. Semua detail telah diverifikasi terhadap source code, bukan sekadar desain teoretis.*
