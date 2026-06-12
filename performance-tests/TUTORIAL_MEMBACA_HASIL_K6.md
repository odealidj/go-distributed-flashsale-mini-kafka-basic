# Tutorial: Membaca & Memahami Hasil Uji K6

Dokumen ini ditujukan bagi *Engineer* yang ingin memahami bagaimana cara membaca *output* terminal dari *load test* k6 yang ada di proyek ini.

---

## 1. Memahami Struktur Output Dasar K6

Ketika Anda menjalankan pengujian k6, Anda akan melihat sebuah blok teks rangkuman (Summary) di akhir eksekusi. Ada beberapa metrik utama yang harus Anda perhatikan:

### A. HTTP Metrics
```text
http_req_duration..............: avg=202.84ms p(90)=328.43ms p(95)=369.15ms
http_req_failed................: 96.00% 4800 out of 5000
http_reqs......................: 5000   1000/s
```
* **`http_req_duration`**: Ini adalah waktu yang dibutuhkan dari saat permintaan dikirim hingga seluruh respons diterima. Metrik yang paling dilihat di industri adalah **`p(95)`** (persentil 95), artinya 95% dari total *request* selesai dalam waktu di bawah angka tersebut. Semakin kecil angkanya, semakin responsif server Anda.
* **`http_req_failed`**: Menunjukkan persentase *request* yang dianggap gagal (status HTTP >= 400). **Penting:** Dalam uji coba Flash Sale, angka kegagalan yang tinggi (misal 96%) **adalah hal yang wajar dan diharapkan**. Ini terjadi karena setelah stok 200 habis, sisa 4800 *request* akan ditolak dengan `409 Conflict` (Stok Habis) atau `429 Too Many Requests` (Rate Limit).
* **`http_reqs`**: Total beban HTTP yang diproses oleh server, beserta jumlah rata-rata per detik (RPS).

### B. Custom Thresholds (Kriteria Kelulusan)
Di skrip k6 proyek ini, kami menggunakan *Thresholds* otomatis:
```text
✓ 'p(95)<5000' p(95)=369.15ms
```
Jika batas (contohnya latensi P95 tidak boleh lebih dari 5 detik di bawah beban maksimal) terlampaui, k6 akan memberikan tanda silang (✗) merah dan menggagalkan *pipeline* CI/CD.

---

## 2. Cara Membaca Hasil per Skenario

### 🌊 Skenario 1: Thundering Herd (Nginx Rate Limiter)
* **Tujuan**: Mencegah server lumpuh (*down*) akibat ribuan orang melakukan klik secara bersamaan.
* **Apa yang Diharapkan**:
  Anda akan melihat nilai `http_req_failed` sangat tinggi (bisa mendekati 99%). Ini karena Nginx `limit_req` mengintersep dan langsung memblokir ribuan permintaan berlebih tersebut sebelum mereka menyentuh API Gateway, mengembalikan `429 Too Many Requests`.
* **Kunci Keberhasilan**: P95 sangat rendah (misal: 7ms). Backend selamat, trafik dibendung di depan.

### 🚫 Skenario 2: No-Oversell (The Golden Test)
* **Tujuan**: Menguji apakah sistem bisa kebobolan stok (*oversell*) jika 5000 pengguna memesan 200 stok dalam milidetik yang sama.
* **Apa yang Diharapkan**:
  Akan muncul kotak tabel kustom di terminal:
  ```text
  ║  ✅ 202 Accepted:   200 (checkout berhasil)         ║
  ║  ⚠️  409/429    :  4800 (stok habis / rate limited) ║
  ║  ❌ Error Sistem:     0                             ║
  ```
* **Kunci Keberhasilan**: Jumlah `202 Accepted` **harus persis sama** dengan `INITIAL_STOCK` (dalam contoh ini, 200). Tidak boleh 201 atau lebih.

### 🚀 Skenario 3: Absolute Breakpoint Limit
* **Tujuan**: Menekan sistem perlahan-lahan hingga hancur/tumbang untuk mengetahui limit infrastruktur saat ini.
* **Apa yang Diharapkan**:
  K6 akan menaikkan RPS (Request per Second) dari 0 hingga 3000 RPS.
* **Kunci Keberhasilan**: Memperhatikan kapan metrik `Error Sistem (500/Timeout)` mulai muncul. Dalam uji coba *production-grade* lokal kita, sistem bertahan sempurna di **3.000 RPS dengan 0.00% Error** (latensi P95: 364ms).

### 🔄 Skenario 4: Idempotency (Mencegah Double Checkout)
* **Tujuan**: Memastikan pengguna nakal yang menekan tombol bayar 3x berturut-turut dengan ID yang sama hanya terhitung 1x transaksi.
* **Apa yang Diharapkan**:
  Akan ada metrik kustom `idempotency_failures` di ringkasan akhir.
* **Kunci Keberhasilan**: `idempotency_failures.......: 0`.

---

## Kesimpulan Eksekutif
Bagi tim arsitek, gabungan dari metrik di atas membuktikan bahwa sistem ini tidak hanya **cepat (P95 < 500ms)**, tetapi juga **secara matematis aman (Zero Oversell & Idempotent)**, meskipun dijalankan dalam mode skala mikro (lokal Docker Compose).
