# Panduan Grafana Dashboards: Flash Sale & Observability

Dokumen ini berisi *best practice* dalam menyusun panel metrik di Grafana khusus untuk arsitektur sistem *Flash Sale* terdistribusi kita. Semua *query* di bawah ini dirancang agar sejalan dengan *load test* k6 yang kita jalankan (seperti *Thundering Herd* dan pengujian *Idempotency*).

---

## Metadata Dashboard (Saat Menyimpan / Save)
Saat Anda pertama kali menekan tombol **Save** di Grafana, isilah kotak dialog yang muncul dengan data berikut agar rapi dan representatif:
* **Title:** `Flash Sale Monitoring Center`
* **Description:** `Dashboard komprehensif untuk memantau performa microservices, tingkat kesuksesan checkout (Thundering Herd), antrean outbox Kafka, serta utilisasi hardware (Goroutines & RAM) selama event Flash Sale.`
* **Folder:** `Dashboards` (atau biarkan default)

---

## 1. Grup Panel: Business & Core Flash Sale Metrics
Tujuan dari grup panel ini adalah untuk memberikan indikator langsung mengenai jalannya "bisnis" saat momen Flash Sale sedang berlangsung.

### Panel 1.1: Flash Sale Checkout Status (Success vs Out of Stock)
* **Panel Title:** "Flash Sale Checkout Status"
* **Panel Description:** "Memantau rasio keberhasilan checkout (stok didapatkan) berbanding dengan penolakan massal akibat kehabisan stok (Out of Stock) secara real-time."
* **Visualisasi:** Time series (Line chart) atau Bar gauge.
* **PromQL:**
  ```promql
  sum(rate(flashsale_checkout_total[1m])) by (status)
  ```
* **Maksud & Tujuan:**
  Memantau secara *real-time* nasib ratusan ribu pengguna yang mencoba berbelanja.
  - Saat `success` naik, artinya stok barang sedang diperebutkan dan berkurang (Redis Lua Script bekerja).
  - Saat `failed_out_of_stock` melonjak tajam bak gunung meletus, ini merepresentasikan *Thundering Herd* di mana mayoritas permintaan ditolak mentah-mentah secara efisien (tanpa hit ke PostgreSQL) karena barang sudah ludes.
* **Korelasi k6:** Sangat cocok dipantau saat menjalankan `04_no_oversell.js` dan `03_checkout_pubsub.js`.

### Panel 1.2: Transactional Outbox Kafka Relay Throughput
* **Panel Title:** "Outbox Relay Throughput"
* **Panel Description:** "Memantau kecepatan dan status pengiriman event asinkronus (Saga Pattern) dari database relasional ke Kafka Broker."
* **Visualisasi:** Time series.
* **PromQL:**
  ```promql
  sum(rate(flashsale_outbox_relay_total[1m])) by (status, event_type)
  ```
* **Maksud & Tujuan:**
  Sistem kita menggunakan pola **Transactional Outbox**. Saat terjadi *checkout*, data disimpan di PostgreSQL dan diteruskan ke Kafka oleh *Relay Worker* di latar belakang.
  Panel ini bertugas sebagai radar utama untuk mendeteksi apakah *Worker* sukses menyedot data ke Kafka (`status="sent"`) atau ada kegagalan komunikasi ke broker Kafka (`status="failed"`).
* **Korelasi k6:** Memastikan integritas *Saga Pattern* saat menjalankan `05_compensation_test.js`.

---

## 2. Grup Panel: System Throughput (Traffic)
Tujuan dari grup panel ini adalah memantau seberapa berat trafik yang menghantam *load balancer* dan di-*routing* oleh API Gateway ke masing-masing *microservice*.

### Panel 2.1: API Gateway Requests Per Second (RPS)
* **Panel Title:** "API Gateway RPS"
* **Panel Description:** "Melihat lonjakan total beban trafik (Thundering Herd) yang menghantam API Gateway berdasarkan rute/operasi."
* **Visualisasi:** Time series (Line).
* **PromQL:**
  ```promql
  sum(rate(kratos_requests_total{service_name="api-gateway"}[1m])) by (operation)
  ```
* **Maksud & Tujuan:**
  API Gateway adalah gerbang depan utama sistem kita. Panel ini akan membedah trafik berdasarkan endpoint (misal `/api.v1.Checkout/PubSub` vs `/api.v1.Products/List`). Anda bisa melihat lonjakan ribuan RPS masuk secara harfiah.
* **Korelasi k6:** Sesuai dengan pengujian `03_soak_test.js` dan simulasi ribuan *Virtual Users* secara mendadak.

### Panel 2.2: Microservices Workload Distribution
* **Panel Title:** "Microservices Workload"
* **Panel Description:** "Distribusi persentase beban request di antara seluruh microservice yang ada di dalam klaster."
* **Visualisasi:** Pie Chart (dengan opsi: *Calculate -> Last*).
* **PromQL:**
  ```promql
  sum(rate(kratos_requests_total[1m])) by (service_name)
  ```
* **Maksud & Tujuan:**
  Sistem *microservices* mendistribusikan tugas. Grafik Pie ini akan memperlihatkan porsi kerja. Saat Flash Sale, porsi `inventory-service` (untuk menahan serangan *checkout*) dan `order-service` (untuk asinkronus Saga) harus mendominasi dibandingkan layanan lain.

---

## 3. Grup Panel: Latency & Quality of Service (QoS)
Menjaga latensi tetap rendah adalah harga mati untuk sistem Flash Sale agar tidak *timeout* di sisi Nginx atau Client.

### Panel 3.1: API P95 Latency (SLA Monitoring)
* **Panel Title:** "P95 System Latency"
* **Panel Description:** "Metrik QoS (Quality of Service) - Menunjukkan kecepatan respons maksimal untuk 95% pengguna. SLA sehat berada di bawah 500ms."
* **Visualisasi:** Time series.
* **PromQL:**
  ```promql
  histogram_quantile(0.95, sum(rate(kratos_request_duration_seconds_bucket[1m])) by (le, service_name))
  ```
* **Maksud & Tujuan:**
  "P95" berarti *95% dari seluruh request selesai lebih cepat dari nilai di grafik ini*. Jika P95 melampaui `0.5` detik (500ms), artinya *server* Anda mulai kesulitan (CPU/Memory penuh) atau ada masalah koneksi database.
* **Korelasi k6:** Tes k6 kita memiliki Threshold bawaan: `'http_req_duration': ['p(95)<500']`. Panel ini memvisualisasikan aturan otomatis dari k6 tersebut secara kasat mata.

### Panel 3.2: System Error Rate (%)
* **Panel Title:** "System Error Rate"
* **Panel Description:** "Persentase error teknis internal sistem (seperti Redis down atau Kafka gagal). Penolakan out-of-stock tidak dihitung sebagai error di sini."
* **Visualisasi:** Stat (Angka tunggal raksasa) atau Bar gauge.
* **PromQL:**
  ```promql
  (sum(rate(kratos_errors_total[1m])) / sum(rate(kratos_requests_total[1m]))) * 100
  ```
* **Maksud & Tujuan:**
  Ini harus bernilai `0%`. Jika angka ini mendadak naik > 1%, artinya terjadi kegagalan sistem murni (misalnya koneksi Redis terputus, PostgreSQL *deadlock*, atau Kafka *down*). Perlu diingat, penolakan "Out of Stock" (HTTP 409) yang normal TIDAK akan masuk ke sini jika diset sebagai *expected error*.

## 4. Grup Panel: Resource & Hardware Utilization (Go Runtime)
Tujuan dari grup panel ini adalah memastikan tidak ada *memory leak* atau jumlah goroutine yang meledak tak terkendali selama beban tinggi.

### Panel 4.1: Goroutines per Service (CPU Load & Leak Detection)
* **Panel Title:** "Goroutines per Service"
* **Panel Description:** "Mendeteksi indikasi kebocoran Goroutine (Goroutine Leak) dengan memantau jumlah thread Go yang aktif pada setiap layanan."
* **Visualisasi:** Time series (Line chart)
* **Query:**
  ```promql
  sum(go_goroutine_count) by (service_name)
  ```
* **Maksud & Tujuan:**
  Memantau jumlah *thread* ringan (Goroutine) yang aktif di setiap layanan Go. Lonjakan Goroutine yang tidak turun-turun setelah *Flash Sale* selesai menandakan adanya *Goroutine Leak* (misal: koneksi menggantung atau *channel* tidak ditutup).

### Panel 4.2: Memory Allocation per Service (OOM Detection)
* **Panel Title:** "Memory Allocation per Service"
* **Visualisasi:** Time series (Format Data: Bytes)
* **Query:**
  ```promql
  sum(go_memory_used_bytes) by (service_name)
  ```
* **Maksud & Tujuan:**
  Memantau penggunaan memori (RAM) aktual dari setiap layanan. Sangat penting untuk mendeteksi *Out of Memory* (OOM).

---

## Ringkasan Tata Letak yang Disarankan
Saat Anda membuat Grafana Dashboard, susunlah Panel dalam bentuk "Grid" dengan hirarki berikut untuk ruang kontrol (*War Room*) Flash Sale:
1. **Baris (Row) Atas:** P95 Latency (Line), System Error Rate (Stat), API Gateway RPS (Line). *(Health Checks)*
2. **Baris (Row) Tengah:** Flash Sale Checkout Status (Line besar di tengah layar). *(Business Check)*
3. **Baris (Row) Bawah:** Outbox Relay Throughput (Line) & Microservices Workload (Pie). *(Async Processing Check)*
4. **Baris (Row) Paling Bawah:** Goroutines & Heap Memory (Line). *(Infrastructure & Resource Check)*

---

## 5. Panduan Membaca Grafik & Deteksi Anomali (Incident Playbook)

Sebagai seorang *Engineer*, Anda tidak hanya melihat garis yang naik-turun, tetapi Anda harus bisa menerjemahkannya menjadi sebuah tindakan (*actionable insights*). Berikut adalah panduan cara membaca grafik saat event Flash Sale:

### 1. Lonjakan Garis `failed_out_of_stock` pada *Checkout Status*
* **Yang Anda Lihat:** Garis hijau/kuning menembus langit dalam hitungan detik.
* **Artinya:** Ini **BUKAN** sebuah error! Ini adalah bukti sistem *Thundering Herd Protection* Anda (Redis Lua Script) bekerja dengan sempurna menolak ratusan ribu pengguna dalam milidetik setelah kuota habis.
* **Tindakan:** Nikmati pemandangannya. Periksa apakah garis `success` jumlahnya sama persis dengan kuota barang Flash Sale yang dijanjikan.

### 2. Garis `status="sent"` (Kuning) pada *Outbox Relay* Berhenti atau Datar Padahal Checkout Masih Jalan
* **Yang Anda Lihat:** *Checkout Status* naik, tetapi *Outbox Relay* datar (0) atau memunculkan garis `failed`.
* **Artinya:** Terjadi sumbatan leher botol (*bottleneck*) atau putus koneksi antara Relay Worker dan Kafka Broker. Data *checkout* tertahan di PostgreSQL dan lambat masuk ke *Event-Driven* ekosistem.
* **Tindakan:** Cek kontainer `kafka` (apakah *down*?), atau *scale-up* jumlah Goroutine pada Relay Worker untuk mempercepat sedotan data.

### 3. P95 Latency Tiba-Tiba Tembus Angka 1-2 Detik (Biasanya di bawah 0.5s)
* **Yang Anda Lihat:** Garis P95 Latency pada API Gateway melonjak naik dan tidak turun-turun.
* **Artinya:** API Gateway menunggu terlalu lama dari *backend service*, atau CPU pada Nginx/Gateway sudah menyentuh limit 100%.
* **Tindakan:** Segera lihat *Microservices Workload* dan *Goroutines per Service*. Servis mana yang memakan resource paling tinggi? Lakukan *Horizontal Pod Autoscaling* (tambah kontainer) pada servis tersebut.

### 4. *System Error Rate* Melonjak ke Angka > 0%
* **Yang Anda Lihat:** Grafik Error Rate yang tadinya datar di `0` tiba-tiba naik ke `0.2` atau lebih.
* **Artinya:** Ada permintaan yang digagalkan oleh sistem karena masalah teknis (contoh: PostgreSQL kehabisan koneksi, Redis *Timeout*, atau *panic/crash* di kode Go).
* **Tindakan:** Ini adalah status SIAGA 1. Segera buka log dari *microservices* menggunakan perintah `podman logs <nama-service>` untuk menemukan *stack trace* error-nya.

### 5. Garis Memori (RAM) Terus Menanjak Bak Tangga (*Staircase Pattern*) Tanpa Turun
* **Yang Anda Lihat:** Pada grafik *Memory Allocation per Service*, garis terus naik (meskipun trafik sudah mereda/selesai) dan tidak pernah dibersihkan (GC/Garbage Collection tidak efektif).
* **Artinya:** Anda sedang mengalami **Memory Leak** yang parah. Kemungkinan besar ada *map* global, *cache* yang tidak pernah *expire*, atau Goroutine yang menggantung (buka koneksi DB tanpa `defer close`).
* **Tindakan:** Jika RAM hampir menabrak batas kapasitas server (OOM Kill), segera *restart* service terkait secara manual. Setelah Flash Sale selesai, analisa profil memori (pprof) kode Go Anda.
