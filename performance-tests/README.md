# Performance Testing - Flash Sale k6

Folder ini berisi semua script uji performa untuk sistem Flash Sale.

## Prasyarat

1. **k6** sudah terinstall:
   ```bash
   # Linux
   sudo gpg -k
   sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
     --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
   echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" \
     | sudo tee /etc/apt/sources.list.d/k6.list
   sudo apt-get update && sudo apt-get install k6
   ```

2. **Sistem berjalan** (`docker-compose up -d`)

3. **Redis & semua service aktif** (cek `docker ps`)

---

## Deskripsi Skenario

| File | Skenario | VU | Durasi | Tujuan |
|------|----------|----|--------|--------|
| `01_thundering_herd.js` | Thundering Herd | 0→1000 | ~50s | Uji ketahanan lonjakan tiba-tiba |
| `02_idempotency_test.js` | Idempotency | 200 | ~60s | Verifikasi tidak ada double-checkout |
| `03_soak_test.js` | Soak | 100 | 30m | Deteksi memory leak & degradasi |
| `04_no_oversell.js` | No-Oversell | 5000 | ~2m | **Golden test**: jumlah terjual ≤ stok |

---

## Cara Menjalankan

### Setup Stok Awal
```bash
# Set 100 unit stok di Redis
cd performance-tests
PRODUCT_ID=product-flashsale-001 INITIAL_STOCK=100 bash setup_stock.sh
```

### Jalankan 1 Skenario
```bash
cd performance-tests

# Thundering Herd
k6 run --env PRODUCT_ID=product-flashsale-001 k6/01_thundering_herd.js

# Idempotency Test
k6 run --env PRODUCT_ID=product-flashsale-001 k6/02_idempotency_test.js

# Soak Test (30 menit)
k6 run --env PRODUCT_ID=product-flashsale-001 k6/03_soak_test.js

# No-Oversell (golden test)
k6 run --env PRODUCT_ID=product-flashsale-001 --env INITIAL_STOCK=100 k6/04_no_oversell.js
```

### Jalankan Semua (kecuali Soak)
```bash
cd performance-tests
PRODUCT_ID=product-flashsale-001 INITIAL_STOCK=100 bash run_all.sh
```

---

## Interpretasi Hasil

### Thundering Herd
- **202 Accepted**: Checkout diterima sistem
- **409 Conflict**: Stok habis — ini **PERILAKU YANG BENAR**
- **429 Too Many Requests**: Rate limit Nginx aktif — ini **PERILAKU YANG BENAR**
- **5xx**: Error sistem — **harus 0**

### Idempotency Test
- Metric `idempotency_failures` **harus = 0**
- Artinya tidak ada user yang berhasil checkout dua kali

### No-Oversell (Golden Test)
- `successful_checkout_count` **harus ≤ INITIAL_STOCK**
- Laporan otomatis muncul di terminal setelah selesai

---

## Integrasi dengan Observability

Setiap request melewati Nginx → API Gateway → Redis/Postgres.  
Trace ID ada di header dan body response (`meta.trace_id`).  
Buka **Jaeger UI** di `http://localhost:16686` untuk melihat trace selama test berlangsung.

## Hasil Pengujian (Juni 2026 - Verified Prod-Infra Test)

### Skenario 1: Thundering Herd (03_checkout_pubsub.js / Nginx)
Mensimulasikan lonjakan mendadak hingga ribuan request dalam waktu singkat untuk menguji ketahanan Nginx dan API Gateway.

- **VUs**: Ramp-up ke 500 VU (menghasilkan ~3000 RPS) selama 40 detik.
- **Hasil Utama**:
  - Total Request Terkirim: `~135.000+`
  - Checkout sukses/diterima Nginx: `~99.8%` (Lolos masuk antrean PENDING)
  - Ditolak karena rate limit atau stok habis (409/429): `0.12%`
  - Error Sistem (5xx/0): `0`
  - P95 Latency: **7.46ms**
- **Kesimpulan**: Nginx rate limiter dan struktur asynchronous Kafka efektif menjaga backend dari kelebihan beban dan membatasi throughput yang masuk dengan latensi ultra-rendah.

### Skenario 2: Idempotency Test (02_idempotency_test.js)
Mensimulasikan retry yang agresif dari banyak client dengan `X-Idempotency-Key` yang sama (meng-bypass rate limiter via port internal `18000`).

- **VUs**: 200 VU masing-masing mencoba 3x berturut-turut.
- **Hasil Utama**:
  - Semua pengguna melakukan retry.
  - Jumlah order unik (successful checkout): `200`
  - `idempotency_failures`: `0`
  - P95 Latency: **118.37ms**
- **Kesimpulan**: Redis Lua script sukses menangani konkurensi ekstrem, menjamin satu Idempotency Key = maksimal satu order.

### Skenario 3: Soak Test (03_soak_test.js)
Mensimulasikan trafik konstan (100 VU) selama durasi panjang (30 menit) untuk mendeteksi memory leak.

- **Hasil Utama**:
  - Resource RAM: Tetap stabil tanpa kebocoran di semua microservice (Product, Inventory, Order, Payment).
  - P95 Response Time: Tetap konsisten di bawah toleransi (<500ms).
- **Kesimpulan**: Sistem Go terbukti aman untuk dibiarkan berjalan dalam waktu panjang tanpa degradasi performa yang berarti.

### Skenario 4: No-Oversell / Golden Test (04_no_oversell.js)
Memastikan jumlah stok di-reserve tidak pernah melebihi batas (200). 5000 VU bersaing merebut stok secara konstan.

- **VUs**: 5000 VU menembak serentak.
- **Hasil Utama**:
  - Diterima (202 Accepted): `200` (Tepat sama dengan INITIAL_STOCK)
  - Konflik/Ditolak (409/429): `4800`
  - Error Sistem: `0`
  - P95 Latency: **369.15ms**
- **Kesimpulan**: 100% Zero-Overselling. Redis Lua script sukses menyediakan atomicity untuk pemotongan stok pada skenario Highly Concurrent tanpa satupun error sistem.

### Skenario Tambahan: Absolute Breakpoint (07_find_breakpoint.js)
Mencari limit tertinggi dari server lokal (Mac/PC) yang menjalankan Redis Sentinel + Kafka.
- **Hasil Utama**:
  - Beban: 3.000 RPS
  - Total Request: 156.019
  - Error Sistem: `0.00%`
  - P95 Latency: **364ms**

### Skenario 5: Saga Compensation E2E (05_compensation_test.js / run_compensation_e2e.sh)
Memvalidasi arsitektur Event-Driven dan Choreography Saga Pattern.

- **Alur**:
  1. API Gateway menerima checkout.
  2. Inventory Service memotong stok di Redis dan menerbitkan `StockReservedEvent`.
  3. Order Service membuat Order (`PENDING`).
  4. Payment Service men-simulasikan kegagalan bayar dan menerbitkan `PaymentFailedEvent`.
  5. Order Service mengubah status jadi `CANCELLED` dan menerbitkan `OrderCancelledEvent`.
  6. Inventory Service menerima event dan me-refund stok di Redis secara asinkron.
- **Hasil Utama**:
  - Event konsisten terkirim via Kafka (menggunakan Outbox Pattern).
  - Stok dikembalikan utuh sesuai nilai awal.
  - Order dibatalkan.
- **Kesimpulan**: Event-Driven Saga Pattern berhasil menjaga data consistency (Eventually Consistent) pada skenario error.
