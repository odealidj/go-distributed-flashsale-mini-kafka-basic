# Docker Compose Deep Dive (Configuration Guide)

Dokumen ini adalah **Panduan Bedah Konfigurasi** (*Line-by-Line Breakdown*) untuk membantu Anda memahami secara mendalam apa yang sebenarnya terjadi di dalam file `docker-compose.yml` pada sistem *Flash Sale* ini.

Tujuannya adalah sebagai bahan pembelajaran (*learning material*) bagi *Backend/DevOps Engineer* untuk mengerti mengapa sebuah konfigurasi ditulis sedemikian rupa, bukan hanya sekadar *copy-paste*.

---

## 1. Anatomi Dasar File `docker-compose.yml`

Secara umum, file Docker Compose kita memiliki 3 blok utama (Top-Level Elements):

1. **`services:`** 
   Blok ini mendefinisikan aplikasi atau kontainer apa saja yang ingin kita jalankan (misal: postgres, redis, api-gateway).
2. **`volumes:`**
   Berfungsi untuk **Persistensi Data**. Kalau kontainer dihapus, data di dalam database akan hilang. Dengan mendefinisikan *Named Volume* (seperti `pgdata:`), Docker akan menyimpan data di hard disk *host* secara aman di luar siklus hidup kontainer.
3. **`networks:`**
   Mendefinisikan ruang jaringan terisolasi. Di sistem kita, namanya adalah `flashsale_net` dengan *driver* tipe `bridge`. Artinya, semua kontainer di dalam *network* ini bisa saling berkomunikasi dengan aman, tetapi terisolasi dari *network* host (komputer Anda) maupun dari internet luar secara langsung.

---

## 2. Bedah Konfigurasi Layanan Aplikasi (*Go Services*)

Mari kita bedah secara tuntas layanan `api-gateway` sebagai representasi utama dari cara layanan Go kita di-_deploy_.

```yaml
  api-gateway:
    build:
      context: .
      dockerfile: ./api-gateway/Dockerfile
```
* **`build`**: Memberitahu Docker bahwa *image* untuk layanan ini belum ada di internet (Docker Hub). Kita meminta Docker untuk **membangunnya sendiri secara lokal**.
* **`context: .`**: Menentukan "titik awal folder" (*build context*) yang akan dikirim ke Docker engine. Titik `.` berarti *current directory* (folder root proyek). Ini **sangat penting** di sistem *Go Workspace* kita! Karena di dalam *Dockerfile*, aplikasi butuh mengkopi file dari luar folder `api-gateway` (misalnya folder `shared/` dan `go.work`). Jika *context*-nya diset menjadi `./api-gateway`, proses *build* pasti gagal karena Docker tidak punya akses ke folder induknya.
* **`dockerfile: ...`**: Lokasi absolut atau relatif dari instruksi `Dockerfile` tersebut berada.

```yaml
    container_name: flashsale-${API_GATEWAY_HOST:-api-gateway}
    profiles: ["app"]
```
* **`container_name`**: Nama unik kontainer yang muncul saat Anda mengetik `docker ps`.
* **`profiles`**: Ini adalah fitur "Grup". Dengan menyematkan profil `["app"]`, layanan ini **tidak akan ikut menyala** saat Anda hanya mengetik `docker compose up -d`. Layanan ini baru akan jalan jika secara eksplisit Anda panggil `docker compose --profile app up -d`. Ini sangat berguna jika Anda hanya ingin menyalakan infrastrukturnya (DB/Kafka) dan menjalankan kodenya secara lokal via IDE.

```yaml
    depends_on:
      postgres:
        condition: service_healthy
      kafka:
        condition: service_started
```
* **`depends_on`**: Mengatur urutan *startup* (nyala). `api-gateway` tidak boleh menyala sebelum infrastrukturnya siap.
* **`service_started`**: Hanya memastikan kontainer Kafka sudah berstatus *running*, meskipun Kafka belum tentu sudah 100% siap menerima pesan.
* **`service_healthy`**: Jauh lebih ketat! Ini memastikan bahwa kontainer `postgres` tidak hanya menyala, tetapi fitur *Healthcheck*-nya (seperti perintah pengecekan koneksi SQL) sudah sukses dijalankan dan membalas status OK.

```yaml
    ports:
      - "${API_GATEWAY_PORT:-18000}:${API_GATEWAY_INTERNAL_PORT:-8000}"
```
* **`ports`**: Membuka jalur akses dari Laptop Anda (*Host*) ke dalam Kontainer. Format aslinya adalah `"HOST_PORT : CONTAINER_PORT"`.
* **Sintaks `${A:-B}` (Variabel Default)**: Cara membacanya: *"Ambil nilai dari variabel `API_GATEWAY_PORT` di file `.env`. Tapi jika di `.env` kosong atau tidak ada, gunakan angka `18000` sebagai *fallback/default*."* Ini membuat konfigurasi kita **dinamis** dan tidak di-*hardcode*.

```yaml
    volumes:
      - ./certs/public.pem:/app/certs/public.pem:ro
```
* **Bind Mount (`volumes`)**: Kita mengkopi/melekatkan file kriptografi (*Public Key JWT*) dari laptop lokal `./certs/public.pem` ke dalam kontainer di path `/app/certs/public.pem`.
* **`:ro` (Read-Only)**: Ini adalah fitur keamanan tambahan! Menjamin bahwa kontainer `api-gateway` hanya bisa membaca file tersebut, tetapi tidak akan pernah bisa mengedit atau menghapusnya secara tidak sengaja.

```yaml
    environment:
      - APP_ENV=${APP_ENV:-development}
      - PRODUCT_SERVICE_ENDPOINT=${PRODUCT_SERVICE_ENDPOINT:-product-service:9001}
```
* **`environment`**: Menyuntikkan konfigurasi (Variabel Lingkungan) ke dalam program Go yang sedang berjalan (diambil via `os.Getenv()`).
* **Service Discovery Otomatis (`product-service:9001`)**: Docker memiliki **DNS Server Internal**. Jika `api-gateway` ingin menghubungi `product-service` via gRPC, ia tidak perlu tahu *IP Address*-nya. Cukup sebutkan nama layanannya `product-service`, dan DNS Docker otomatis meneruskan permintaan tersebut ke kontainer yang bersangkutan. Oleh karena itu nilai _default_ URL-nya adalah `product-service:9001`.

---

## 3. Bedah Konfigurasi Infrastruktur & Observabilitas

Konfigurasi infrastruktur sering kali memanfaatkan opsi lanjutan dari Docker Compose:

### a. Healthcheck Database
```yaml
  postgres:
    ...
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U root -d flashsale_master"]
      interval: 5s
      timeout: 5s
      retries: 5
```
* **`healthcheck`**: Ini adalah mekanisme agar Docker secara berkala (setiap 5 detik, `interval: 5s`) mengetes apakah database benar-benar siap menerima kueri.
* **`pg_isready`**: Merupakan aplikasi _native_ PostgreSQL yang ringan untuk mengecek konektivitas database. Jika gagal berturut-turut hingga 5 kali (`retries: 5`), Docker akan melabeli kontainer ini sebagai *Unhealthy*.

### b. Membatasi Memori via Argumen Command
```yaml
  redis:
    ...
    command: redis-server --maxmemory 512mb --maxmemory-policy allkeys-lru
```
* **`command`**: Mengesampingkan (menimpa) perintah _startup_ bawaan dari *image* Docker. Di sini kita dengan sengaja menginstruksikan Redis untuk membatasi RAM hanya di **512 MB**, dengan kebijakan `allkeys-lru` (menghapus _cache_ paling lama secara otomatis jika RAM penuh).

### c. Mengatasi Ketergantungan *Root* (Privilege)
```yaml
  loki:
    image: docker.io/grafana/loki:latest
    user: "0"  # Jalankan sebagai root
```
* **`user: "0"`**: Dalam sistem operasi Linux/Docker, *User ID* (UID) 0 artinya akses **Root** (Admin Tertinggi). Beberapa sistem (khususnya *Podman* atau Linux dengan SELinux aktif) dapat memblokir Loki untuk menulis *log* ke folder di *host* jika ia menggunakan UID _non-privileged_. Akses root diset agar Loki memiliki izin penuh menulis metrik tersebut.

---

## 4. Pola *Networking*: Internal vs External

Saat melihat `ports` di layanan Infrastruktur seperti Kafka, Anda akan menemui format kompleks:

```yaml
    ports:
      - "127.0.0.1:19094:9094"
```

1. **`127.0.0.1`**: Ini disebut **Host Binding**. Artinya, port 19094 ini **HANYA** bisa diakses dari dalam *localhost* (laptop kita sendiri). Ini mencegah orang dari luar jaringan (misal: di satu jaringan Wi-Fi cafe yang sama) mengakses dan menyadap Kafka broker lokal milik Anda!
2. **`19094`**: Port di Host/Laptop.
3. **`9094`**: Port asli di dalam kontainer Docker.

### Mengapa Kafka memiliki Port Terpisah (Internal vs Eksternal)?
Di dalam konfigurasi Kafka `KAFKA_CFG_LISTENERS`:
* **`PLAINTEXT://:9092` (Internal)**: Digunakan oleh layanan Go (seperti `order-service`) yang hidup **di dalam** Docker Network.
* **`EXTERNAL://:9094` (External)**: Digunakan oleh Kafka UI atau program pihak ketiga yang diakses dari **luar** Docker. Kafka memiliki mekanisme unik di mana klien *broker* butuh diberitahu di alamat IP dan Port spesifik apa mereka bisa mengobrol. Inilah fungsi dari argumen *Advertised Listeners*.

---

## 5. Ringkasan & Best Practice 

Dari file `docker-compose.yml` kita, ada beberapa prinsip *Best Practice* tingkat produksi yang dipraktikkan langsung di lokal:

1. **Isolation First**: Tidak ada port *Database* atau *Cache* yang diekspos ke publik (`0.0.0.0`), semuanya dibatasi melalui `127.0.0.1`.
2. **Idempotent Startups**: Penggunaan skrip `init-dbs.sh` untuk menyiapkan 5 buah *logical database* dilakukan otomatis oleh Postgres saat pertama kali melakukan *boot*. Ini menghindari pembuatan *database* secara manual.
3. **Observability Out-of-the-Box**: Konfigurasi Promtail/Loki dan OpenTelemetry sudah dirangkai satu *network*, sehingga log lokal terserap sempurna tanpa ada program ekstra yang perlu dipasang (*install*) di laptop pengembang.
