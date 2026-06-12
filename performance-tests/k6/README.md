# Panduan Load Testing & Kinerja K6 (Flash Sale)

Dokumen ini menjelaskan struktur, tujuan, dan metode eksekusi dari rangkaian pengujian (Load Testing) K6 yang digunakan dalam proyek ini.

*Load testing* di arsitektur *Flash Sale* sangatlah kritis karena sistem akan menerima lonjakan trafik *(thundering herd)* dalam hitungan detik. K6 digunakan untuk memastikan keandalan *microservices*, penjaga gerbang Nginx, antrean Kafka, dan akurasi Redis Lua Script.

---

## 1. Topologi Target Eksekusi (Dual Target)

Kita menggunakan teknik *environment variable* `TARGET` di K6 agar satu set skrip bisa memiliki dua tujuan berbeda.

### A. Target Nginx (Frontend Protection)
* **Tujuan**: Menguji ketahanan *Rate Limiter* (`limit_req_zone`) milik Nginx.
* **Karakteristik**: Memaklumi dan mengharapkan penolakan massal. Mendapatkan status `HTTP 429 Too Many Requests` dianggap sebagai keberhasilan sistem dalam melindungi dirinya.
* **Perintah Eksekusi**: 
  ```bash
  make test-load-nginx
  ```

### B. Target Backend (Core Microservices Stress)
* **Tujuan**: Mem-*bypass* Nginx dan langsung menyiksa API Gateway. Skrip ini didesain untuk mengetahui "sampai di titik apa *backend* (Goroutine, Kafka, Postgres, Redis) akan hancur?".
* **Karakteristik**: Sama sekali **TIDAK** menoleransi HTTP 429. Semua pesanan harus masuk antrean Kafka (HTTP 202) atau ditolak karena kehabisan stok yang sah (HTTP 409).
* **Perintah Eksekusi**: 
  ```bash
  make test-load-backend
  ```

---

## 2. Skenario Uji Komparasi Arsitektur (*Checkout Flow*)

Pengujian ini bertujuan membandingkan seberapa efisien 4 jenis pendekatan arsitektur klien-server dalam menangani respon asinkron dari sistem berbasis *Event-Driven* (Kafka Saga).

| Skrip | Deskripsi Cara Kerja | Karakteristik Beban (Grafana) |
|---|---|---|
| **01_raw_checkout_polling.js** | Klien melakukan checkout, mendapat status `PENDING`, lalu terus-menerus (*spamming*) melakukan HTTP GET (polling) setiap 500ms hingga pesanan sukses. | **RPS sangat tinggi.** Memakan beban **CPU** paling banyak pada API Gateway karena harus memproses ratusan kueri GET kosong per detik. |
| **02_checkout_long_polling.js** | Klien melakukan koneksi HTTP ke server. Server menahan koneksi tersebut (menggantungnya) hingga mendapat kepastian dari Kafka, barulah membalas dengan status final. | **RPS rendah**, namun **Goroutines** dan *Active TCP Connections* akan meningkat drastis. Berpotensi memakan RAM tinggi. |
| **03_checkout_pubsub.js** | Menggunakan pola Pub/Sub dan Go Channel internal. Begitu status Kafka selesai, notifikasi didorong melalui WebSocket / Channel efisien. | **Paling Seimbang**. CPU dan RAM tetap stabil meskipun terjadi *thundering herd*. |
| **04_checkout_sse.js** | Menggunakan teknologi **Server-Sent Events (SSE)**. Koneksi tetap terbuka satu arah (dari server ke klien) untuk mendorong (*push*) status akhir secara langsung. | Mengonsumsi sedikit lebih banyak memori untuk mempertahankan *stream*, namun unggul dalam latensi (*Real-time*). |

*(Perintah `make test-load-backend` secara otomatis menjalankan keempat skrip ini secara berurutan agar Anda bisa mengamati perbandingannya di Grafana).*

---

## 3. Skenario Uji Coba Kritis & Ekstrim

Selain performa arsitektur asinkron, k6 juga digunakan untuk memvalidasi algoritma "Anti-Overbooking" dan keamanan transaksi *Microservices Saga*.

### 02_idempotency_test.js (Double-Spend Protection)
* **Tujuan**: Memastikan pengguna nakal / pengguna dengan koneksi internet lambat yang melakukan "Double Click" saat *checkout* tidak akan membuat dua pesanan atau mengurangi stok sebanyak dua kali.
* **Cara Kerja**: 1 Klien yang sama akan menembakkan *Checkout API* berulang-ulang dengan JWT Token dan *Idempotency-Key* yang persis sama secara beruntun.
* **Ekspektasi**: Hanya **1** request yang diteruskan ke Kafka. Sisanya langsung ditolak (*Drop/Idempotency Error*).
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/02_idempotency_test.js
  # atau
  make test-idempotency
  ```

### 03_soak_test_3m.js (Warm-up / Short Soak Test)
* **Tujuan**: Memastikan konfigurasi Grafana, Prometheus, dan Rate Limiter bekerja dengan baik sebelum melakukan Soak Test sesungguhnya.
* **Cara Kerja**: Beban konstan (300 VU) dijalankan selama **3 menit**.
* **Ekspektasi**: Semua sistem berjalan tanpa error dan metrik masuk ke dasbor Grafana.
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/03_soak_test_3m.js
  # atau
  make test-soak-3m
  ```

### 03_soak_test.js (Memory Leak Detection)
* **Tujuan**: Mendeteksi masalah yang hanya terjadi dalam jangka panjang (misalnya Goroutines Leak, kehabisan *Connection Pool* database, atau RAM habis perlahan-lahan).
* **Cara Kerja**: Beban tidak terlalu tinggi, tetapi konstan dan dijalankan dalam waktu **sangat lama** (misal: 30 menit atau lebih).
* **Ekspektasi**: P95 Latensi tidak boleh berubah dari menit pertama hingga menit ke-30. RAM tidak membentuk kurva tangga naik (*Staircase*).
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/03_soak_test.js
  # atau
  make test-soak
  ```

### 04_no_oversell.js (The Golden Standard Test)
* **Tujuan**: Ujian tersulit dan paling penting untuk sistem *Flash Sale*. Membuktikan bahwa stok barang **TIDAK PERNAH** terpotong hingga angka minus (minus stock / overselling).
* **Cara Kerja**: Stok diset `100`. Lalu 5000 klien berbeda menyerang bersamaan secara harfiah di milidetik yang sama.
* **Ekspektasi**: K6 akan menjumlah akumulasi log. Total pesanan dengan HTTP 202 **wajib** tepat `100` atau kurang. Sisa 4900+ kueri harus mental dengan HTTP 409 (Stok Habis).
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/04_no_oversell.js
  # atau
  make test-no-oversell
  ```

### 05_compensation_test.js (Saga Rollback)
* **Tujuan**: Memvalidasi proses *Saga Orchestrator* bekerja ketika terjadi insiden/kegagalan.
* **Cara Kerja**: Skrip akan membuat pesanan (mengurangi stok awal), namun saat di fase pembayaran (*Payment*), skrip celaja akan mengirim uang bayaran palsu / gagal.
* **Ekspektasi**: Sistem otomatis memicu pembatalan saga (*Rollback*) yang berakhir pada pulihnya stok di Redis kembali menjadi angkat normal secara transparan.
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/05_compensation_test.js
  # atau
  make test-compensation
  ```

### 06_flashsale_spike_prod.js (End-to-End Flash Sale Spike)
* **Tujuan**: Menguji ketahanan seluruh alur Saga (Checkout → Kafka → Polling Order → Payment) di bawah simulasi beban lonjakan (*spike*) sesungguhnya, yang disesuaikan dengan limitasi *hardware* (seperti Ryzen 5900HS) dan infrastruktur produksi (Redis Sentinel & Kafka 10 partisi).
* **Cara Kerja**: Menggunakan `ramping-arrival-rate` untuk secara cepat mendongkrak trafik menjadi ratusan *request* per detik, bukan sekadar membuka ribuan goroutine sekaligus (yang berisiko membuat laptop *crash*). Skrip menelusuri alur transaksi hingga selesai.
* **Ekspektasi**: Membuktikan bahwa Kafka dengan 10 partisi sanggup menyelesaikan pembuatan pesanan dengan P95 latency rendah, dan seluruh arsitektur *asynchronous* berfungsi normal di tengah lonjakan beban (*thundering herd*).
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/06_flashsale_spike_prod.js
  # atau
  make test-spike-prod
  ```

### 07_find_breakpoint.js (Mencari Limit / Stress Test)
* **Tujuan**: Mencari tahu di titik (*Requests Per Second*) berapakah sistem (API Gateway, Redis, PostgreSQL, atau Kafka) mulai *crash* atau melambat drastis (P95 Latency > 2 detik).
* **Cara Kerja**: Beban awal sangat kecil (50 RPS), lalu terus meningkat bagaikan tangga selama 2,5 menit hingga mencapai angka yang sangat ekstrem (3.000 RPS). Menggunakan stok buatan 1.000.000 agar Kafka dan Postgres dipaksa bekerja terus-menerus memproses *event*.
* **Ekspektasi**: Tes ini secara sengaja didesain untuk **gagal**. Tujuannya adalah mengamati grafik Grafana untuk melihat komponen mana yang mentok ke 100% CPU atau kehabisan memori lebih dulu, sehingga kita tahu komponen mana yang harus di-*scale* jika trafik riil meledak.
* **Perintah Eksekusi**:
  ```bash
  k6 run -e TARGET=backend performance-tests/k6/07_find_breakpoint.js
  # atau
  make test-breakpoint
  ```

---
**Tip:** Selalu jalankan `make infra-up` dan jalankan Grafana (`http://localhost:3000`) sebelum mengeksekusi skrip apa pun untuk melihat fenomena aslinya secara visual!

**Penting - Menerapkan Perubahan Kode (Rebuild):**
Jika Anda baru saja mengubah *source code* Golang Anda (misalnya memperbaiki logika PubSub atau SSE), sistem tidak akan menyadari perubahan tersebut sampai Anda mem-*build* ulang *container* aplikasi.
Gunakan perintah ini setiap kali Anda selesai menulis/memperbaiki kode:
```bash
docker-compose --profile app up -d --build
```
*(Atau Anda bisa menggunakan `make down` lalu `make up` untuk mereset seluruh ekosistem).*
