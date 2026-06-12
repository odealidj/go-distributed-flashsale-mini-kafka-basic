# Panduan Setup & Pembuatan Dashboard Grafana (Step-by-Step)

Dokumen ini adalah tutorial *step-by-step* untuk pemula dalam menghubungkan Grafana dengan Prometheus, serta panduan langkah demi langkah cara membuat panel visualisasi dari nol menggunakan UI Grafana versi terbaru (versi 11.x ke atas).

---

## 1. Login dan Akses Awal
1. Pastikan Anda sudah menjalankan infrastruktur Docker dengan `make up`.
2. Buka browser dan ketik alamat: **[http://localhost:3000](http://localhost:3000)**
3. Masukkan kredensial bawaan:
   - **Username:** `admin`
   - **Password:** `admin`
4. Grafana akan meminta Anda mengubah kata sandi. Anda bisa menggantinya atau mengklik **Skip** untuk melewati.

---

## 2. Menghubungkan Data Source (Prometheus)
Grafana hanyalah penampil data (visualisasi). Kita harus memberitahu Grafana untuk mengambil metrik dari Prometheus. Langkah ini hanya perlu dilakukan **satu kali** saja.

1. Pada menu sebelah kiri (Sidebar), cari opsi **Connections** (ikon colokan listrik) dan pilih **Data sources**.
2. Klik tombol biru **Add data source** di kanan atas.
3. Pilih **Prometheus** dari daftar yang muncul.
4. Pada halaman konfigurasi Prometheus, temukan kotak **Prometheus server URL** (di bawah bagian *Connection*).
5. Ketikkan URL internal Docker dari Prometheus: 
   ```text
   http://prometheus:9090
   ```
6. *Scroll* ke bagian paling bawah layar dan klik tombol **Save & test**.
7. Pastikan muncul notifikasi berwarna hijau bertuliskan **"Successfully queried the Prometheus API."** (Artinya Grafana berhasil terhubung!).

---

## 3. Cara Membuat Dashboard & Panel Baru (Versi UI Terbaru)

Ikuti langkah-langkah ini untuk setiap grafik (Panel) yang ingin Anda buat sesuai dengan [Panduan Metrik PromQL](grafana-dashboards.md).

### A. Membuat Dashboard Kosong
1. Di kiri atas (atau di sejajar dengan kolom pencarian *Search*), klik ikon **+** (tanda tambah) dan pilih **New dashboard**.
2. Anda akan melihat layar kosong berlatar belakang gelap.

### B. Menambahkan Panel Metrik
1. Perhatikan bilah menu sempit di sisi **sebelah kanan layar**.
2. Cari bagian bertuliskan **Add**, lalu tepat di bawah tulisan **Panel** (*Drag or click to add a panel*), klik ikon kotak bergambar grafik dengan tanda **+** biru muda.
3. Otomatis, layar akan terbelah menjadi dua. Bagian atas adalah pratinjau grafiknya, dan bagian bawah adalah tempat Anda menulis *query*.

### C. Konfigurasi Rumus PromQL
1. Pastikan kotak menurun (*dropdown*) **Data source** yang berada tepat di kiri bawah (di atas kotak kode) sudah memilih **Prometheus**.
2. Cari kolom input berukuran besar yang bertuliskan **Metrics browser** (atau berlabel **A** / **Query A**).
3. Ketikkan (atau *copy-paste*) rumus metrik yang Anda inginkan. Misalnya:
   ```promql
   sum(rate(flashsale_checkout_total[1m])) by (status)
   ```
4. Tekan **Enter** di keyboard Anda (atau klik area mana saja di luar kotak input).
5. Agar grafik terlihat lonjakannya, ganti **Rentang Waktu** di sisi kanan atas layar (misalnya dari *Last 6 hours* diubah menjadi **Last 15 minutes**).

### D. Mengubah Visualisasi (Opsional)
Secara bawaan, Grafana akan membuat grafik garis (*Time series*). Jika Anda ingin menggantinya:
1. Lihat kembali panel panjang di **sebelah kanan layar**.
2. Temukan area **Visualization** (biasanya berada di barisan paling atas dari panel pengeditan sebelah kanan).
3. Ubah dari *Time series* menjadi tipe lain seperti **Bar gauge**, **Pie chart**, atau **Stat** (sesuaikan dengan jenis metrik).

### E. Mengganti Nama Panel
1. Masih di bilah konfigurasi **sebelah kanan layar**, gulir (*scroll*) ke bawah dan temukan area **Panel options**.
2. Cari kolom **Title**.
3. Hapus tulisan bawaan *"Panel Title"* dan ganti dengan judul yang lebih baik (misalnya: `"Checkout Success Rate"`).

### F. Menerapkan Panel (Apply)
Setelah Anda puas dengan tampilannya:
1. Klik tombol **Apply** di sudut **kanan atas**.
2. Anda akan dikembalikan ke layar *Dashboard* utama dan panel pertama Anda akan muncul di layar.
3. Ulangi proses di atas (mulai dari *Langkah B: Menambahkan Panel*) untuk memasukkan semua rumus-rumus metrik lainnya dari dokumen panduan.

### G. Mengelompokkan Panel dengan Row (Baris)
Agar *dashboard* terlihat profesional dan tidak sesak, Anda bisa mengelompokkan beberapa panel ke dalam sebuah folder *collapsible* yang disebut **Row**.
1. Di bilah menu utama atas (sejajar dengan tombol kalender/waktu), klik tombol **Add** (ikon **+**).
2. Pilih **Row** dari menu *dropdown*. Sebuah baris horizontal berjudulkan "Row title" akan muncul di layar.
3. Arahkan kursor ke ujung kanan tulisan "Row title" tersebut hingga muncul ikon **gerigi kecil (⚙️)**, lalu klik.
4. Ubah **Title** dengan nama grup (misalnya: `"1. Grup Panel: Business & Core Flash Sale Metrics"`), lalu tekan *Enter* / Update.
5. Untuk memasukkan grafik ke dalam grup, arahkan kursor ke bagian atas grafik Anda hingga kursor berubah bentuk menjadi tangan (ikon *drag*).
6. **Klik dan seret (drag)** grafik tersebut ke tepat di bawah garis Row.
7. Sekarang, Anda bisa mengeklik ikon panah 🔽 di sisi kiri Row untuk melipat (*collapse*) atau membuka (*expand*) seluruh grafik secara bersamaan!

---

## 4. Menyimpan Dashboard
Sangat penting! Jika Anda tidak menyimpan *dashboard* ini, Anda akan kehilangan semuanya saat me-*refresh* browser.

1. Klik tombol biru **Save** (ikon disket) di deretan paling kanan atas layar.
2. Sebuah jendela kecil (pop-up) akan muncul.
3. Di kolom **Dashboard name**, ketikkan nama kumpulan panel Anda (Contoh: `"Flash Sale Command Center"`).
4. Biarkan opsi Folder berada di `"General"`.
5. Klik tombol **Save** sekali lagi.

Selesai! Sekarang, kapanpun Anda membuka menu **Dashboards** di sisi kiri layar Grafana, *dashboard* racikan Anda akan selalu ada di sana.
