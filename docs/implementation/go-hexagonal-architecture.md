# Panduan Go Hexagonal Architecture (Ports and Adapters)

Dokumen ini menjelaskan prinsip arsitektur yang digunakan di seluruh *microservices* proyek ini. Memahami dokumen ini adalah **prasyarat** sebelum berkontribusi ke kode.

> [!NOTE]
> Semua contoh kode, nama file, dan nama package dalam dokumen ini diambil dari kode aktual di repositori ini.

---

## 1. Konsep Utama: Mengapa Hexagonal?

Tanpa arsitektur yang jelas, sebuah *service* yang bermula sederhana akan cepat menjadi "Big Ball of Mud" — kode HTTP handler yang langsung memanggil SQL query, logika bisnis yang tersebar di mana-mana, dan hampir mustahil di-*test* tanpa database nyata.

*Hexagonal Architecture* (juga dikenal sebagai *Ports and Adapters*, diperkenalkan oleh Alistair Cockburn) memecahkan masalah ini dengan **satu aturan tunggal**: **Dependency hanya boleh mengalir ke dalam** — dari layer luar ke layer dalam. Layer dalam (Domain) tidak boleh tahu menahu tentang layer luar (Postgres, Redis, Kafka, REST).

```
+-----------------------------------------------------------+
|                        ADAPTERS                           |
|  (gRPC Server, REST Handler, PostgreSQL, Redis, Kafka)    |
|                                                           |
|    +-------------------------------------------------+    |
|    |                 APPLICATION                     |    |
|    |   (Usecase, Port Interface, Background Worker)  |    |
|    |                                                 |    |
|    |      +-----------------------------------+      |    |
|    |      |             DOMAIN                |      |    |
|    |      |  (Model/Entity, Domain Events)    |      |    |
|    |      +-----------------------------------+      |    |
|    +-------------------------------------------------+    |
+-----------------------------------------------------------+
```

**Manfaat langsung yang dirasakan:**
- Unit test usecase **tanpa** database — cukup mock interface `port.OrderRepository`
- Ganti database dari PostgreSQL ke MySQL? Cukup tulis adapter baru, tidak perlu ubah usecase
- Isolasi kegagalan — bug di adapter Redis tidak bisa "menular" ke domain logic

---

## 2. Bedah Layer (Berdasarkan Kode Aktual)

### A. Layer Domain (`internal/domain/model/`)

Jantung dari aplikasi. **Bebas dari library eksternal** — tidak ada `sqlx`, `pgx`, `kratos`, `franz-go`, atau bahkan `context` dari library eksternal.

Berisi struct yang merepresentasikan **entitas bisnis murni** dan **Kafka event** yang melintas antar service.

**Contoh aktual — `order-service/internal/domain/model/order.go`:**
```go
// Entitas bisnis
type Order struct {
    ID          string    `db:"id"`
    UserID      string    `db:"user_id"`
    ProductID   string    `db:"product_id"`
    Quantity    int       `db:"quantity"`
    TotalAmount int64     `db:"total_amount"`
    Status      string    `db:"status"` // PENDING, PAID, CANCELLED
    CreatedAt   time.Time `db:"created_at"`
    UpdatedAt   time.Time `db:"updated_at"`
}

// Kafka event (juga domain model — mendefinisikan "bahasa" antar service)
type StockReservedEvent struct {
    EventID   string `json:"event_id"`
    UserID    string `json:"user_id"`
    ProductID string `json:"product_id"`
    Quantity  int    `json:"quantity"`
    Price     int64  `json:"price"`
}

type OrderCancelledEvent struct {
    EventID   string `json:"event_id"`
    OrderID   string `json:"order_id"`
    ProductID string `json:"product_id"`
    Quantity  int    `json:"quantity"`
    Reason    string `json:"reason"`
}
```

> [!NOTE]
> Tag `db:` digunakan oleh `sqlx` untuk mapping kolom DB ke struct field. Tag ini **tidak melanggar** prinsip domain karena tidak mengimpor library apapun — hanya metadata string.

---

### B. Layer Application (`internal/application/`)

Menjembatani antara dunia luar (Adapter) dan dunia dalam (Domain). Berisi tiga sub-layer:

#### `port/` — Interface Definitions (Kontrak)

Berisi **definisi interface** yang menjadi kontrak antara Application dan Adapter. Dengan interface ini, usecase tidak pernah tahu implementasi teknisnya (apakah PostgreSQL, Redis, atau mock).

**Contoh aktual — `order-service/internal/application/port/order_repository.go`:**
```go
type OrderRepository interface {
    CreateOrderIdempotent(ctx context.Context, order *model.Order, eventID string) (*model.Order, error)
    UpdateOrderStatusIdempotent(ctx context.Context, orderID, status, eventID string) (*model.Order, error)
    GetOrder(ctx context.Context, orderID string) (*model.Order, error)
    CancelOrderAndEmitEvent(ctx context.Context, order *model.Order, event *model.OrderCancelledEvent) error
}
```

#### `usecase/` — Business Logic Orchestration

Mengorkestrasi logika bisnis: memanggil fungsi domain, memanggil port interface, **tidak pernah** memanggil library eksternal secara langsung.

**Contoh aktual — `order-service/internal/application/usecase/order_saga.go`:**
```go
// HandleStockReserved dipanggil saat ada event StockReservedEvent dari Kafka
func (uc *OrderSagaUsecase) HandleStockReserved(ctx context.Context, event *model.StockReservedEvent) error {
    order := &model.Order{
        ID:          uuid.New().String(),
        UserID:      event.UserID,
        ProductID:   event.ProductID,
        Quantity:    event.Quantity,
        TotalAmount: int64(event.Quantity) * event.Price,
        Status:      "PENDING",
    }
    // Panggil port interface — tidak peduli apakah implementasinya PostgreSQL atau mock
    _, err := uc.repo.CreateOrderIdempotent(ctx, order, event.EventID)
    return err
}
```

#### `worker/` dan `job/` — Background Workers (Sub-layer Tambahan)

Beberapa service memiliki sub-layer tambahan di dalam Application untuk background goroutine:

| Service | Sub-layer | File | Fungsi |
|---|---|---|---|
| Order Service | `application/worker/` | `timeout_worker.go` | Cancel pesanan PENDING > 15 menit |
| Inventory Service | `application/job/` | `reconciliation_job.go` | Deteksi dan refund stock leak |

Background worker ini **tetap di layer Application** karena mereka menggunakan port interface, bukan mengakses database secara langsung.

---

### C. Layer Adapter (`internal/adapter/`)

Menghubungkan aplikasi dengan dunia nyata. Dibagi menjadi dua arah:

#### `inbound/` — Driver / Primary Adapters

Menerima *request* dari luar dan memanggil Usecase:

| Sub-folder | Teknologi | Contoh File | Service |
|---|---|---|---|
| `grpc/` | go-kratos gRPC | `inventory_server.go` | Inventory, Product, Payment, Auth |
| `rest/` | go-kratos HTTP | `handler.go` | API Gateway saja |
| `kafka/` | franz-go | `consumer.go` | Order, Inventory |

#### `outbound/` — Driven / Secondary Adapters

Implementasi konkret dari interface yang didefinisikan di `port/`:

| Sub-folder | Teknologi | Fungsi |
|---|---|---|
| `postgres/` | `sqlx` + raw SQL | Implementasi repository interface ke PostgreSQL |
| `redis/` | `go-redis` + Lua Script | Operasi atomik stok (ReserveStock, RefundStock) |
| `grpc/` | go-kratos gRPC client | Memanggil service lain (di API Gateway: Circuit Breaker + Timeout) |

**Contoh aktual — `inventory-service/internal/adapter/outbound/redis/lua_script.go`:**
```go
// Implementasi port.RedisPort → Adapter ke Redis
type redisAdapter struct {
    client *redis.Client
    reserveScript *redis.Script
    refundScript  *redis.Script
}

// ReserveStock mengimplementasikan port.RedisPort.ReserveStock()
func (r *redisAdapter) ReserveStock(ctx context.Context, productID, eventID string, qty int) (bool, error) {
    result, err := r.reserveScript.Run(ctx, r.client, keys, args...).Int()
    // ...
}
```

---

### D. Dependency Injection (`cmd/main.go` + Wire)

`cmd/main.go` (atau `wire.go`) adalah satu-satunya tempat di mana semua layer "dirakit" bersama. Di sinilah Dependency Injection terjadi.

**Siapa yang pakai Wire (`google/wire`) vs Manual DI:**

| Service | DI Method |
|---|---|
| `inventory-service` | ✅ `google/wire` (`wire.go` + `wire_gen.go`) |
| `product-service` | ✅ `google/wire` |
| `payment-service` | ✅ `google/wire` |
| `auth-service` | ❌ Manual DI di `main.go` |
| `order-service` | ❌ Manual DI di `main.go` |
| `api-gateway` | ❌ Manual DI di `main.go` |

---

## 3. Kasus Khusus: Auth Service

Auth Service memiliki struktur domain yang sedikit berbeda dari service lain karena dikembangkan secara bertahap:

```
auth-service/internal/
├── application/
│   ├── domain/
│   │   └── user.go          ← User struct di sini (bukan di internal/domain/model/)
│   ├── port/
│   │   └── payment_repository.go
│   └── usecase/
│       └── auth_usecase.go
├── adapter/
│   ├── inbound/grpc/auth_server.go
│   └── outbound/postgres/user_repo.go
└── domain/model/
    └── payment.go           ← File ini perlu direfactor ke auth/user model
```

> [!WARNING]
> Auth Service menempatkan `User` struct di `application/domain/user.go` (bukan `domain/model/`), dan memiliki `domain/model/payment.go` yang merupakan sisa dari scaffolding awal. Ini adalah **technical debt** yang perlu direfactor agar konsisten dengan service lain.

---

## 4. Aturan Emas (DOs and DON'Ts)

### ✅ LAKUKAN

| Aturan | Alasan |
|---|---|
| Definisikan interface untuk DB/messaging di `application/port/` | Memungkinkan mock dan DI |
| Konversi protobuf struct ke domain model di Inbound Adapter | Domain tidak boleh bergantung pada protobuf |
| Kembalikan domain error (`errors.New(...)`) dari Usecase | Adapter yang menerjemahkan ke HTTP code |
| Tulis unit test Usecase dengan mock port | Tidak perlu DB/Redis nyata untuk test bisnis logic |
| Letakkan background worker di `application/worker/` atau `application/job/` | Masih layer Application karena pakai port interface |

### ❌ JANGAN LAKUKAN

| Larangan | Dampak jika dilanggar |
|---|---|
| Import `sqlx`, `redis`, `kgo` di layer `domain/` atau `application/usecase/` | Domain terikat pada infrastruktur → tidak bisa di-test unit |
| Return HTTP status code (`400`, `404`) dari Usecase | Bocornya HTTP concern ke Business Logic |
| Gunakan struct protobuf gRPC di layer Domain atau Application | gRPC struct berubah jika proto berubah → cascade break |
| Bawa `*sqlx.Tx` ke layer Application secara langsung | Bocornya DB concern ke Business Logic — gunakan pattern seperti Unit of Work atau Transactional Repository |
| Akses database langsung dari Inbound Adapter (handler) | Melewati Usecase layer → tidak ada business rules |

---

## 5. Alur Data: Dari Request ke Response

Berikut alur lengkap sebuah request `POST /checkout` melewati semua layer:

```
[Client HTTP Request]
        ↓
[Inbound Adapter: rest/handler.go]
  - Parse JSON body
  - Validasi input (format, required fields)
  - Extract userID dari JWT
  - Panggil usecase.Checkout(ctx, productID, userID)
        ↓
[Application Usecase: gateway_usecase.go]
  - Panggil inventoryClient.ReserveStock() via port interface
  - Evaluasi hasil (true/false)
  - Return nil error (sukses) atau domain error
        ↓
[Outbound Adapter: grpc/clients.go]
  - Wrap call dalam Circuit Breaker (gobreaker)
  - Set context timeout 3 detik
  - Kirim gRPC request ke Inventory Service
        ↓
[Response kembali ke handler]
  - Usecase return nil → handler return HTTP 202
  - Usecase return error "stok habis" → handler return HTTP 409
  - Adapter return gobreaker.ErrOpenState → handler return HTTP 503
```
