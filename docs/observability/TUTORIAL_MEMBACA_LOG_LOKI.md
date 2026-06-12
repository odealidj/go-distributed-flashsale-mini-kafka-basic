## 1. Persiapan: Mengirim Log ke Loki

### Arsitektur (Podman Rootless)
```
Container → journald → journald-to-loki.sh → /tmp/flashsale-logs/*.log → Promtail → Loki → Grafana
```

Karena sistem ini menggunakan **Podman Rootless**, container tidak bisa menggunakan *syslog driver* secara langsung. Log container dikirim ke `journald` (default Podman), lalu dibaca oleh skrip bridge yang menulis ke file per service, dan Promtail membacanya dari file tersebut.

### Langkah Menjalankan Log Collection

**Langkah 1:** Pastikan Loki & Promtail berjalan:
```bash
docker-compose up -d loki promtail
```

**Langkah 2:** Jalankan skrip bridge journald→file (dijalankan di HOST, bukan di dalam container):
```bash
# Jalankan sekali saja, berjalan di background
nohup bash deploy/journald-to-loki.sh > /tmp/journald-bridge.log 2>&1 &
echo "Bridge PID: $!"
```

**Verifikasi:**
```bash
# Cek apakah file log sudah terbuat
ls -lah /tmp/flashsale-logs/

# Cek apakah Loki sudah menerima log
curl http://localhost:3100/loki/api/v1/labels
```

> **Catatan Penting:** Skrip `journald-to-loki.sh` perlu dijalankan ulang setiap kali server/laptop di-restart. Untuk otomatisasi, bisa didaftarkan ke systemd user service (opsional).


---

## 2. Cara Mengakses Loki di Grafana

1. Buka Dasbor Grafana di browser (`http://localhost:3000`).
2. Di menu sebelah kiri, klik ikon **Kaca Pembesar (Explore)**.
3. Pada *dropdown* pilihan Datasource di pojok kiri atas (biasanya tertulis Prometheus), ubah menjadi **Loki**.
4. Anda akan melihat antarmuka pencarian log.

---

## 3. Dasar-Dasar Pencarian Log (LogQL)

Loki menggunakan bahasa kueri yang disebut **LogQL** (sangat mirip dengan PromQL). Pencarian di Loki dibagi menjadi dua bagian: **Log Stream Selector** (mencari berdasarkan sumber/tag) dan **Log Pipeline** (menyaring isi teks).

### A. Memfilter Berdasarkan Nama Servis
Klik tombol **"Label filters"** atau ketik langsung di kolom pencarian:
```logql
{service_name="api-gateway"}
```
*(Tekan `Shift + Enter` atau klik tombol `Run query` untuk melihat hasilnya).*

### B. Mencari Teks / Kata Kunci Tertentu
Gunakan simbol `|=` untuk mencari teks yang mengandung kata tertentu.
```logql
{service_name="order-service"} |= "error"
```

### C. Mencari Trace ID (Sangat Penting untuk Microservices)
Karena aplikasi Golang Anda sudah menggunakan *Distributed Tracing* (Jaeger/OpenTelemetry), setiap request yang melintas antar servis akan memiliki `trace_id` yang sama. Anda bisa melacak satu transaksi dari hulu ke hilir dengan kueri ini:

```logql
{service_name=~".+"} |= "7b9c21df8df3345a"
```
*(Ini akan mencari `trace_id` tersebut di **seluruh** servis secara bersamaan. Anda bisa melihat perjalanan request dari `api-gateway` -> `order-service` -> `payment-service` dalam urutan waktu yang rapi).*

### D. Mengabaikan Teks Tertentu (Exclude)
Gunakan simbol `!=` untuk membuang log yang tidak relevan (misalnya *healthcheck*).
```logql
{service_name="api-gateway"} != "health" != "ping"
```

---

## 4. Keunggulan Grafana Loki

* **Korelasi Metrik dan Log (Seamless Integration):** 
  Jika Anda sedang melihat Dasbor "Go Metrics" dan melihat ada lonjakan penggunaan CPU atau Goroutine pada pukul 14:00, Anda dapat langsung beralih ke menu "Explore" dan Loki akan otomatis menampilkan log yang terjadi tepat pada rentang waktu 14:00 tersebut.
* **Sangat Ringan:**
  Tidak seperti Elasticsearch yang rakus memori karena harus mengindeks semua kata, Loki hanya mengindeks "Label" (seperti `service_name`). Isi teksnya dibiarkan mentah namun sangat cepat dicari menggunakan *grep/filter*.

Selamat menelusuri log Anda secara profesional layaknya seorang *Site Reliability Engineer* (SRE)!
