# Spesifikasi Lengkap Kontrak gRPC Flash Sale

> [!NOTE]
> Dokumen ini dibuat khusus untuk digunakan sebagai **Prompt Context** bagi asisten AI lain (seperti Claude, ChatGPT, Copilot, dll.). Anda dapat menyalin (*copy*) seluruh isi dokumen ini ke dalam kolom obrolan AI untuk memerintahkan AI membangun ulang klien atau server *microservices* dalam bahasa pemrograman lain (Rust, Python, Node.js, dll).

Semua kode di bawah adalah representasi 100% akurat dari kontrak sistem Flash Sale kita.

---

## 1. Auth Service
**File Asli:** `proto/auth/v1/auth.proto`

```protobuf
syntax = "proto3";

package flashsale.auth.v1;

option go_package = "flashsale/auth/api/auth/v1;v1";

service AuthService {
  rpc Register (RegisterRequest) returns (RegisterResponse);
  rpc Login (LoginRequest) returns (LoginResponse);
}

message RegisterRequest {
  string username = 1;
  string password = 2;
}

message RegisterResponse {
  bool success = 1;
  string message = 2;
}

message LoginRequest {
  string username = 1;
  string password = 2;
}

message LoginResponse {
  bool success = 1;
  string access_token = 2;
  string message = 3;
}
```

---

## 2. Inventory Service
**File Asli:** `proto/inventory/v1/inventory.proto`

```protobuf
syntax = "proto3";

package flashsale.inventory.v1;

option go_package = "flashsale/inventory/api/inventory/v1;v1";

service InventoryService {
  // Dipanggil oleh API Gateway saat User menekan tombol beli.
  rpc ReserveStock (ReserveStockRequest) returns (ReserveStockResponse);
  
  // (Internal) Dipanggil jika Order batal atau gagal bayar.
  rpc ReleaseStock (ReleaseStockRequest) returns (ReleaseStockResponse);
}

message ReserveStockRequest {
  string idempotency_key = 1;
  string user_id = 2;
  string product_id = 3;
  int32 quantity = 4;
}

message ReserveStockResponse {
  bool success = 1;
  string event_id = 2; // ID Event Outbox yang terbentuk
  string message = 3;
}

message ReleaseStockRequest {
  string product_id = 1;
  int32 quantity = 2;
}

message ReleaseStockResponse {
  bool success = 1;
}
```

---

## 3. Order Service
**File Asli:** `proto/order/v1/order.proto`

```protobuf
syntax = "proto3";

package flashsale.order.v1;

option go_package = "flashsale/order/api/order/v1;v1";

service OrderService {
  // Dipanggil oleh Gateway untuk melihat status pesanan user
  rpc GetOrder (GetOrderRequest) returns (GetOrderResponse);
}

message GetOrderRequest {
  string order_id = 1;
  string user_id = 2;
}

message GetOrderResponse {
  string order_id = 1;
  string status = 2;
  int64 total_amount = 3;
}
```

---

## 4. Payment Service
**File Asli:** `proto/payment/v1/payment.proto`

```protobuf
syntax = "proto3";

package flashsale.payment.v1;

option go_package = "flashsale/payment/api/payment/v1;v1";

service PaymentService {
  // Dipanggil API Gateway ketika user ingin mendapatkan Virtual Account / link bayar
  rpc ProcessPayment (ProcessPaymentRequest) returns (ProcessPaymentResponse);
}

message ProcessPaymentRequest {
  string order_id = 1;
  string user_id = 2;
  int64 amount = 3;
}

message ProcessPaymentResponse {
  string payment_id = 1;
  string payment_url = 2; // Link Midtrans / Xendit mock
}
```

---

## 5. Product Service
**File Asli:** `proto/product/v1/product.proto`

```protobuf
syntax = "proto3";

package flashsale.product.v1;

option go_package = "flashsale/product/api/product/v1;v1";

service ProductService {
  // Dipanggil API Gateway untuk menampilkan daftar barang flash sale
  rpc ListFlashSaleProducts (ListFlashSaleProductsRequest) returns (ListFlashSaleProductsResponse);
}

message ListFlashSaleProductsRequest {
  int32 page = 1;
  int32 per_page = 2;
}

message ListFlashSaleProductsResponse {
  repeated ProductItem products = 1;
  int32 total_items = 2;
}

message ProductItem {
  string id = 1;
  string name = 2;
  int64 original_price = 3;
  int64 flashsale_price = 4;
}
```
