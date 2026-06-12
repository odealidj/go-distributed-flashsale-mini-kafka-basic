# Peta Background Workers (Pekerja Latar Belakang)

Selain melayani *request* HTTP/gRPC secara langsung (sinkron) dan memproses Kafka Events secara asinkron (*Consumer*), arsitektur *Flash Sale* kita juga sangat bergantung pada sekumpulan program yang berjalan tanpa henti di latar belakang, yang kita sebut sebagai **Background Workers**.

Dokumen ini memetakan secara spesifik semua *worker* yang berjalan di masing-masing *microservices*.

---

## 1. Tiga (3) Relay Worker (Outbox Pattern)

Meskipun kode inti *Relay Worker* hanya ditulis satu kali secara modular di `shared/pkg/outbox/relay.go`, ia diinisialisasi (di-*instantiate*) secara mandiri di tiga layanan berbeda. Karena kita menganut pola *Database per Service*, masing-masing *Relay Worker* ini bertugas untuk menyapu tabel `outbox_messages` di *database* miliknya sendiri.

### A. Inventory Relay Worker
*   **Lokasi Eksekusi:** `inventory-service`
*   **Database Target:** `db_inventory`
*   **Tugas Spesifik:** Membaca *event* stok yang sudah berhasil dipotong, lalu mengirimkan **`StockReservedEvent`** ke Kafka (*topic*: `flashsale.inventory.events`).
*   **Alasan Desain:** Menjamin bahwa Order Service pasti akan mengetahui jika stok sudah berhasil diamankan oleh pengguna.

### B. Order Relay Worker
*   **Lokasi Eksekusi:** `order-service`
*   **Database Target:** `db_order`
*   **Tugas Spesifik:** Membaca pesanan yang dibatalkan (baik karena *timeout* atau gagal bayar), lalu mengirimkan **`OrderCancelledEvent`** ke Kafka (*topic*: `flashsale.order.events`).
*   **Alasan Desain:** Memicu *Saga Compensation*. Event ini akan ditangkap oleh Inventory Service untuk melakukan proses pelepasan stok (*refund stock*) ke Redis Sentinel dan PostgreSQL agar bisa dibeli orang lain.

### C. Payment Relay Worker
*   **Lokasi Eksekusi:** `payment-service`
*   **Database Target:** `db_payment`
*   **Tugas Spesifik:** Membaca status pembayaran yang masuk, lalu mengirimkan **`PaymentCompletedEvent`** atau **`PaymentFailedEvent`** ke Kafka (*topic*: `flashsale.payment.events`).
*   **Alasan Desain:** Memberitahu Order Service untuk mengubah status pesanan menjadi `PAID` (jika sukses) atau `CANCELLED` (jika gagal).

> **Catatan:** Layanan `product-service` dan `auth-service` tidak menjalankan *Relay Worker* karena secara arsitektur, mereka tidak mempublikasikan event kritikal terkait transaksi *checkout* melalui pola *Outbox*.

---

## 2. Satu (1) Timeout Worker

Khusus di dalam `order-service`, terdapat *worker* tambahan yang bertugas sebagai *penjaga waktu* transaksi.

*   **Lokasi Eksekusi:** `order-service`
*   **Database Target:** `db_order`
*   **Cara Kerja:**
    1. Berjalan setiap beberapa detik (secara *polling* periodik).
    2. Menyapu tabel `orders` mencari baris dengan status `PENDING` yang tanggal pembuatannya (`created_at`) sudah melebihi **15 menit** dari waktu sekarang.
    3. Menggunakan teknik **`FOR UPDATE SKIP LOCKED`** untuk menghindari bentrok jika kita menjalankan dua *instance* `order-service` bersamaan (sehingga mereka tidak mengunci baris yang sama).
    4. Mengubah status pesanan tersebut menjadi `CANCELLED`.
    5. **(Krusial)** Menyisipkan perintah pembatalan (*OrderCancelledEvent*) ke dalam tabel `outbox_messages` di `db_order` dalam transaksi yang sama.
*   **Tindak Lanjut:** Setelah `Timeout Worker` menyisipkan pesan ke Outbox, **Order Relay Worker** (lihat poin 1B di atas) akan segera memungut pesan tersebut dan melemparnya ke Kafka agar stok dikembalikan.

---

## 3. Rekonsiliasi Job (Self-Healing)

Walaupun ini lebih bersifat "Job" pemulihan daripada *worker* transaksi aktif, ini adalah pekerja latar belakang terakhir yang krusial.

*   **Lokasi Eksekusi:** `inventory-service`
*   **Target:** `Redis Sentinel` dan `db_inventory` (PostgreSQL)
*   **Tugas Spesifik:** Mencari *stock leak* (kebocoran stok). Jika sebuah slot stok di Redis Sentinel sudah dipotong namun tidak pernah tercatat di tabel `outbox_messages` PostgreSQL setelah lebih dari 5 menit (karena *service crash* di tengah jalan), *job* ini akan mengembalikan stok tersebut ke Redis Sentinel.

---

## Mengapa Pendekatan Tersebar (Distributed) Ini Dipilih?

Awalnya Anda mungkin berpikir: *"Mengapa tidak membuat 1 service khusus (misal: Kafka Publisher Service) untuk mengurus semua ini?"*

Jika kita membuat 1 service khusus yang membaca seluruh database:
1. **Melanggar Enkapsulasi Microservices:** *Service* A tidak boleh menembak/membaca *database service* B secara langsung. Jika skema *database* Inventory berubah, "Kafka Publisher Service" akan ikut rusak (*tight coupling*).
2. **Single Point of Failure (SPOF):** Jika 1 *worker* tersentralisasi itu mati, maka **seluruh** aliran *event* di sistem (baik stok, pembayaran, maupun pesanan) akan berhenti total.
3. **Keterbatasan Koneksi:** *Worker* tersentralisasi harus memelihara koneksi langsung ke 5 *database* yang berbeda, yang sangat buruk untuk performa jaringan.

Dengan menyebarkan *Relay Worker* ke "rumah"-nya masing-masing, arsitektur kita menjadi sangat otonom, *resilient*, dan mudah di-*scale-out*.
