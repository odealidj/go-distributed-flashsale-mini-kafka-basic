# Kontrak Event & Payload

Dokumen ini menjelaskan struktur data event yang dipancarkan ke Kafka oleh masing-masing *microservice*. Semua event serialized sebagai **JSON** dan dikirim via Transactional Outbox Pattern (tersimpan dulu di `outbox_messages`, lalu di-relay oleh Relay Worker).

> [!NOTE]
> Semua field dan struktur payload diverifikasi dari kode domain model dan usecase aktual.

---

## 1. StockReservedEvent

Dipancarkan oleh **Inventory Service** ketika stok berhasil dikurangi di Redis.

- **Topik:** `flashsale.inventory.events`
- **Kafka Key:** `"Inventory"` (dari kolom `aggregate_type` di `outbox_messages`)
- **Consumer:** Order Service — mendengarkan topic ini untuk membuat record order baru
- **Payload:**

| Field | Tipe | Keterangan |
| :--- | :--- | :--- |
| `event_id` | string | ID unik event (UUID — digunakan sebagai idempotency key di Redis dan `processed_events`) |
| `idempotency_key` | string | Digunakan oleh Order Service sebagai `order_id` agar client bisa langsung memakainya untuk proses pembayaran. |
| `user_id` | string | ID pembeli |
| `product_id` | string | ID produk yang di-reserve |
| `quantity` | int | Jumlah item yang di-reserve |

> [!IMPORTANT]
> **Field `price` tidak disertakan** di event ini dari sisi Inventory Service. Akibatnya, properti `event.Price` pada Order Service bernilai default `0`. `TotalAmount` pada database `db_order` akan dicatat sebagai `0` (karena `quantity * 0`). Ini disengaja sebagai penyederhanaan pada versi Flash Sale Basic ini.

> **Catatan:** Field `status: "RESERVED"` yang ada di dokumentasi lama **tidak ada** di payload aktual. Consumer (`order-service/internal/adapter/inbound/kafka/consumer.go`) menggunakan `topic name` untuk membedakan jenis event, bukan field status.

---

## 2. PaymentCompletedEvent

Dipancarkan oleh **Payment Service** saat pembayaran berhasil diproses.

- **Topik:** `flashsale.payment.events`
- **Kafka Key:** `"Payment"` (dari kolom `aggregate_type`)
- **Consumer:** Order Service — mengubah status order menjadi `PAID`
- **Payload:**

| Field | Tipe | Keterangan |
| :--- | :--- | :--- |
| `event_id` | string | ID unik event (UUID) |
| `order_id` | string | ID pesanan yang dibayar |
| `amount` | int64 | Jumlah pembayaran dalam satuan terkecil (sen/rupiah) |

---

## 3. PaymentFailedEvent

Dipancarkan oleh **Payment Service** saat pembayaran gagal.

- **Topik:** `flashsale.payment.events` *(topic yang sama dengan `PaymentCompletedEvent`)*
- **Kafka Key:** `"Payment"`
- **Consumer:** Order Service — membatalkan order dan menerbitkan `OrderCancelledEvent`
- **Payload:**

| Field | Tipe | Keterangan |
| :--- | :--- | :--- |
| `event_id` | string | ID unik event (UUID) |
| `order_id` | string | ID pesanan yang gagal dibayar |
| `amount` | int64 | Jumlah pembayaran yang gagal |
| `reason` | string | Alasan kegagalan — **field ini digunakan consumer untuk membedakan** `PaymentFailedEvent` vs `PaymentCompletedEvent` |

> **Cara consumer membedakan event di topic yang sama:** Order Service consumer membaca field `reason` dari payload JSON. Jika `reason != ""` → `PaymentFailedEvent`. Jika `reason == ""` → `PaymentCompletedEvent`. Lihat `order-service/internal/adapter/inbound/kafka/consumer.go` fungsi `dispatch()`.

> **Simulasi kegagalan:** Pembayaran gagal jika `amount % 10 == 4` (angka yang berakhiran 4 — contoh: 14, 24, 104).

---

## 4. OrderCancelledEvent

Dipancarkan oleh **Order Service** ketika pesanan dibatalkan — karena `PaymentFailedEvent` atau karena timeout 15 menit.

- **Topik:** `flashsale.order.events`
- **Kafka Key:** `"Order"` (dari kolom `aggregate_type`)
- **Consumer:** Inventory Service — mengembalikan stok ke Redis via `RefundStockScript`
- **Payload:**

| Field | Tipe | Keterangan |
| :--- | :--- | :--- |
| `event_id` | string | ID unik event (UUID) |
| `order_id` | string | ID pesanan yang dibatalkan |
| `product_id` | string | ID produk — digunakan Inventory Service untuk tahu key Redis mana yang di-refund |
| `quantity` | int | Jumlah item yang dikembalikan ke stok |
| `reason` | string | Alasan pembatalan — **field ini juga digunakan Inventory consumer** untuk mengidentifikasi bahwa ini adalah `OrderCancelledEvent` (bukan jenis event order lainnya) |

> **Cara Inventory consumer mengidentifikasi event:** Inventory Service consumer membaca field `reason` dari payload. Jika `reason != ""` → diperlakukan sebagai `OrderCancelledEvent` dan `RefundStock` dieksekusi. Lihat `inventory-service/internal/adapter/inbound/kafka/consumer.go` fungsi `dispatch()`.

---

## 5. Alur Routing Event End-to-End

```
Inventory Service
  └─ ReserveStock (Redis Lua) ─→ INSERT outbox_messages
        └─ Relay Worker ─→ Kafka: flashsale.inventory.events
              └─ Order Service Consumer
                    └─ HandleStockReserved() → INSERT orders (PENDING_PAYMENT)
                          └─ INSERT outbox_messages → (jika payment dipanggil)

Payment Service
  └─ ProcessPayment()
        ├─ SUKSES → INSERT outbox_messages (PaymentCompletedEvent, reason="")
        │     └─ Relay Worker → Kafka: flashsale.payment.events
        │           └─ Order Service Consumer
        │                 └─ HandlePaymentCompleted() → UPDATE orders SET status='PAID'
        │
        └─ GAGAL → INSERT outbox_messages (PaymentFailedEvent, reason="...")
              └─ Relay Worker → Kafka: flashsale.payment.events
                    └─ Order Service Consumer
                          └─ HandlePaymentFailed() → UPDATE orders SET status='CANCELLED'
                                └─ INSERT outbox_messages (OrderCancelledEvent)
                                      └─ Relay Worker → Kafka: flashsale.order.events
                                            └─ Inventory Service Consumer
                                                  └─ RefundStock (Redis Lua) ← Saga Compensation
```
