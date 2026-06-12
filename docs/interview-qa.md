# 🎯 Interview Q&A — Flash Sale Distributed System

Dokumen ini berisi kumpulan pertanyaan teknis yang **paling sering ditanyakan** oleh pewawancara Senior/Staff Engineer ketika Anda mempresentasikan atau memperbincangkan proyek ini, lengkap dengan jawaban yang siap diucapkan.

---

## 📌 Cara Menggunakan Dokumen Ini

> Pelajari jawaban di bawah, **jangan dihafal kata per kata**. Pahami konsepnya, lalu ceritakan dengan bahasa Anda sendiri. Interviewers menghargai pemahaman dan *reasoning*, bukan hafalan.

---

## BAGIAN 1: Arsitektur Umum

### Q1: *"Jelaskan secara singkat apa yang dibangun oleh sistem ini dan mengapa arsitekturnya dipilih seperti itu."*

**Jawaban:**

> Saya membangun mini *distributed system* untuk skenario **Flash Sale** — yaitu kondisi di mana ribuan pengguna berebut produk berstock terbatas secara bersamaan dalam hitungan detik.
>
> Core challenge-nya ada dua: mencegah **overselling** (stok minus) dan tetap mempertahankan **checkout response di bawah 100ms** meski ada ribuan *concurrent users*.
>
> Saya memilih **microservices** bukan karena *trendy*, tetapi karena tiap domain memiliki kebutuhan yang berbeda: Inventory butuh atomic operation, Order butuh state machine, Payment butuh saga compensation. Jika dijadikan satu monolit, scaling salah satu layanan akan mengorbankan yang lain.

---

### Q2: *"Mengapa menggunakan Redis Lua Script dan bukan langsung UPDATE di PostgreSQL?"*

**Jawaban:**

> PostgreSQL sangat baik untuk transaksi, namun di bawah tekanan ribuan *concurrent request*, ia akan mengalami **row-level lock contention** — ribuan transaksi antri menunggu giliran untuk mengupdate satu baris data stok yang sama. Hasilnya: latensi melonjak, bahkan *timeout*.
>
> Redis beroperasi *single-threaded* dan menyimpan data di memori. Kecepatan operasi-nya O(1) — tidak peduli berapa banyak *concurrent request* yang datang, setiap operasi tetap selesai dalam mikrodetik.
>
> Saya menggunakan **Lua Script** karena Lua dieksekusi secara atomik di dalam Redis server. Artinya seluruh operasi (cek stok → kurangi stok → simpan idempotency key) terjadi dalam **satu transaksi yang tidak bisa dinterupsi** oleh request lain, sehingga race condition mustahil terjadi.

---

### Q3: *"Apa itu Saga Choreography dan mengapa Anda memilihnya dibanding Orchestration?"*

**Jawaban:**

> Pada transaksi terdistribusi di beberapa service, kita membutuhkan cara untuk menjaga konsistensi data tanpa 2-Phase Commit (2PC) yang sangat lambat dan rawan deadlock.
>
> **Saga** adalah cara memecah satu "big transaction" menjadi serangkaian "local transaction" yang masing-masing berdiri sendiri. Tiap langkah memiliki operasi *kompensasi* jika gagal.
>
> Saya memilih **Choreography** (event-driven, tanpa koordinator pusat) daripada Orchestration karena lebih *loosely-coupled*. Tidak ada satu titik kegagalan tunggal. Setiap service hanya perlu tahu: *"event apa yang perlu aku consume, dan event apa yang aku produce?"* Service tidak perlu tahu satu sama lain. Ini membuat sistem lebih mudah di-scale secara independen.

---

### Q4: *"Apa risiko dari Saga Choreography yang Anda pilih?"*

**Jawaban:**

> Risiko utamanya adalah **kompleksitas debugging**. Ketika ada bug, alur transaksi tersebar di log beberapa service yang berbeda. Untuk mengatasi ini, saya mengimplementasikan **Distributed Tracing dengan OpenTelemetry + Jaeger**. Setiap request mendapatkan `trace_id` unik yang dipropagasikan melalui HTTP header, gRPC metadata, hingga Kafka record header. Jadi satu trace bisa menampilkan perjalanan lengkap sebuah transaksi dari API Gateway hingga Payment Service.
>
> Risiko kedua adalah **partial failure** — bagaimana jika kompensasi juga gagal? Saya mengatasinya dengan DLQ (Dead Letter Queue) dan idempotency guard, sehingga event yang gagal tidak pernah hilang dan selalu bisa di-replay.

---

## BAGIAN 2: Masalah Teknis Spesifik

### Q5: *"Jelaskan Transactional Outbox Pattern. Mengapa Anda menggunakannya?"*

**Jawaban:**

> Ini untuk menyelesaikan masalah klasik **dual-write**: bagaimana cara menjamin bahwa jika data berhasil disimpan di database, event ke Kafka *pasti* ikut terkirim — dan sebaliknya?
>
> Solusi naif adalah: simpan ke DB, lalu publish ke Kafka. Masalahnya: bagaimana jika service crash **setelah** menyimpan ke DB tapi **sebelum** berhasil publish ke Kafka? Event hilang selamanya.
>
> Dengan **Transactional Outbox**: saya menulis event ke tabel `outbox_messages` **di dalam satu database transaction yang sama** dengan data bisnisnya. Jika transaction commit berhasil, event pasti ada di tabel. Kemudian sebuah **Relay Worker** secara periodik membaca tabel ini dan mempublish ke Kafka. Jika Relay Worker crash, ia tinggal membaca ulang tabel outbox yang belum terkirim. **At-Least-Once Delivery** terjamin.

---

### Q6: *"Bagaimana Anda menangani duplicate event di sisi consumer?"*

**Jawaban:**

> Karena kita menggunakan *At-Least-Once Delivery*, ada kemungkinan event yang sama dikirim lebih dari sekali (misalnya karena Relay Worker crash setelah publish tapi sebelum menandai event sebagai "sudah terkirim").
>
> Saya mengimplementasikan **Two-Layer Idempotency**:
> 1. **Layer 1 (Redis):** Sebelum memotong stok, Lua Script mengecek apakah `reserve_idemp:{eventID}` key sudah ada. Jika ada, request langsung ditolak (duplicate).
> 2. **Layer 2 (PostgreSQL):** Sebelum membuat Order baru, saya mengecek tabel `processed_events`. Jika `event_id` sudah ada di tabel itu, transaksi dibatalkan.
>
> Dua layer ini memastikan tidak ada pemotongan stok ganda atau order ganda meskipun event yang sama dikirim berkali-kali.

---

### Q7: *"Apa itu Reconciliation Job dan mengapa Anda membutuhkannya?"*

**Jawaban:**

> Ini adalah skenario edge case yang saya sebut **stock leak**. Bayangkan urutan kejadian ini:
> 1. Inventory Service berhasil memotong stok di Redis (Lua Script berhasil).
> 2. Sebelum sempat menulis ke tabel `outbox_messages` PostgreSQL, service tiba-tiba crash.
> 3. Service restart, tapi stok di Redis sudah terpotong tanpa ada catatan di database.
>
> Hasilnya: stok berkurang, tapi tidak ada order yang terbuat, dan stok itu tidak akan pernah dikembalikan. Ini "uang hilang" dari perspektif bisnis.
>
> **Reconciliation Job** berjalan setiap 1 menit. Ia membandingkan semua `reserve_idemp:*` key yang ada di Redis (yang menyimpan metadata `productID:quantity`) dengan catatan di tabel `outbox_messages`. Jika ada key Redis yang sudah berumur lebih dari 5 menit tapi tidak ada entri di outbox, Job ini otomatis memanggil `RefundStockScript` untuk mengembalikan stok. Ini adalah implementasi nyata dari **Saga Compensating Transaction**.

---

### Q8: *"Mengapa menggunakan gRPC untuk komunikasi antar service dan bukan REST?"*

**Jawaban:**

> Ada dua alasan utama:
> 1. **Performa:** gRPC menggunakan Protocol Buffers (binary format) dan HTTP/2. Payload-nya lebih kecil (bisa 5-10x lebih kecil dari JSON) dan mendukung multiplexing koneksi. Untuk komunikasi *internal* antar service yang bisa dipanggil ribuan kali per detik, ini sangat signifikan.
> 2. **Type Safety:** Kontrak API (`proto` files) menghasilkan kode Go secara otomatis. Tidak ada lagi kesalahan *runtime* karena typo nama field atau tipe data yang tidak cocok. Jika proto berubah, compiler langsung memberitahu mana saja code yang perlu diupdate.
>
> REST tetap saya gunakan di API Gateway untuk komunikasi dengan *external clients* (Browser/Mobile) karena REST lebih universal dan mudah di-debug dengan tools seperti `curl` atau Postman.

---

### Q9: *"Bagaimana Circuit Breaker bekerja di sistem ini?"*

**Jawaban:**

> Circuit Breaker adalah implementasi dari prinsip **Fail Fast**. Saya menggunakan library `sony/gobreaker` di API Gateway.
>
> Bayangkan Inventory Service sedang *down* atau sangat lambat. Tanpa Circuit Breaker, setiap request dari ribuan pengguna ke API Gateway akan menunggu hingga timeout (misalnya 30 detik), menghabiskan goroutine dan memori, lalu baru mengembalikan error. Ini yang disebut **cascading failure**.
>
> Dengan Circuit Breaker, ia berperilaku seperti MCB Listrik:
> - **Closed (Normal):** Request diteruskan ke service.
> - **Open (Rusak):** Setelah ada sejumlah error berturut-turut (threshold), Circuit Breaker "trip". Semua request *langsung* dikembalikan dengan error tanpa perlu menunggu. Service yang sehat tidak terbebani.
> - **Half-Open (Coba Pulih):** Setelah beberapa detik, Circuit Breaker mencoba meneruskan satu request percobaan. Jika berhasil, status kembali ke Closed. Jika gagal, kembali Open.
>
> Hasilnya: API Gateway tetap responsif dan cepat meskipun salah satu backend sedang *down*.

---

## BAGIAN 3: Pertanyaan Skala & Production

### Q10: *"Bagaimana sistem ini di-scale jika traffic meningkat 10x?"*

**Jawaban:**

> Karena setiap service stateless (state hanya ada di Redis, PostgreSQL, dan Kafka), scaling horizontal sangat mudah:
>
> - **Go Services:** Tambah replika pod (misal dari 1 menjadi 3 pod `inventory-service`). Load Balancer akan mendistribusikan traffic secara otomatis. Tidak ada kode yang perlu diubah.
> - **Kafka:** Saya sudah menyiapkan 10 partisi per topic. Ini berarti kita bisa menambah hingga 10 consumer instances secara paralel (1 consumer per partisi). Jika sudah tidak cukup, naikkan jumlah partisi.
> - **Redis:** Untuk Flash Sale, Redis biasanya menjadi bottleneck pertama. Solusinya bisa dengan Redis Cluster (sharding), namun ini memerlukan penggunaan Hash Tags `{key}` pada semua key agar operasi Lua Script (yang multi-key) tetap berjalan di satu node yang sama.
> - **PostgreSQL:** Untuk `inventory-service`, karena mayoritas operasi sudah pindah ke Redis, beban ke Postgres relatif rendah. Namun jika diperlukan, solusinya adalah Read Replicas untuk operasi baca atau PgBouncer sebagai connection pooler.

---

### Q11: *"Apa itu Redis Sentinel dan kapan Anda menggunakannya di proyek ini?"*

**Jawaban:**

> Redis Sentinel adalah solusi **High Availability** untuk Redis Standalone. Saya mengimplementasikannya di `docker-compose.prod.yml`.
>
> Di skenario Flash Sale, Redis adalah komponen *paling kritis* — jika Redis Master mati saat Flash Sale berlangsung, *semua* proses pemotongan stok akan berhenti. Sentinel memastikan hal ini tidak terjadi:
> - 3 proses Sentinel terus memantau Redis Master.
> - Jika Master tidak merespons selama 3 detik, Sentinel mengadakan "pemilihan umum" (quorum = 2/3 setuju) dan mempromosikan salah satu Replica menjadi Master baru.
> - Aplikasi Go saya menggunakan `redis.NewFailoverClient` yang secara otomatis menanyakan ke Sentinel: "Siapa Master saat ini?", sehingga saat failover terjadi, aplikasi Go berpindah koneksi ke Master baru **secara otomatis** dalam hitungan detik tanpa perlu restart.

---

### Q12: *"Bagaimana Anda membuktikan sistem ini benar-benar zero-oversell?"*

**Jawaban:**

> Ini yang paling menarik. Saya menggunakan dua pendekatan:
>
> **1. Unit/Integration Test (Testcontainers-Go):**
> Saya menulis test yang menjalankan 150 goroutine secara bersamaan, semuanya mencoba membeli produk yang sama. Di akhir test, saya memverifikasi bahwa jumlah transaksi sukses + stok tersisa = stok awal. Tidak boleh kurang, tidak boleh lebih.
>
> **2. Load Test (k6) dengan 500 RPS Spike:**
> Saya menjalankan *Spike Test* menggunakan k6, secara instan menembakkan **500 Request Per Detik (RPS)** langsung ke backend (melewati Nginx) di mesin dengan Ryzen 5900HS. Di infrastruktur production-ready (Redis Sentinel HA & Kafka 10 partisi), hasilnya sangat memuaskan:
> - Dari belasan ribu *request* yang masuk dalam sekejap, **tepat 200 request** berhasil melakukan *checkout* sukses (karena stok awal produk di-set persis di angka 200).
> - Sisanya 12.000+ request ditolak secara aman dengan HTTP 409 (Stok Habis).
> - **P95 Latency** untuk pembentukan Order di Kafka hanya sekitar ~2.5 detik di bawah beban ekstrem (*thundering herd*).
> - Sebanyak **200 transaksi** sukses diproses hingga Payment selesai.
> 
> Tidak ada satupun pemotongan stok minus, dan tidak ada satu pun *event* Kafka yang terlewat. Sistem benar-benar *zero-oversell* secara *real-time*.

---

## BAGIAN 4: Pertanyaan "Gotcha" (Penangkap)

### Q13: *"Apa yang terjadi jika Kafka down saat Flash Sale berlangsung?"*

**Jawaban:**

> Ini adalah pertanyaan yang tepat. Dengan Transactional Outbox Pattern, **Kafka down tidak akan menyebabkan data hilang**.
>
> Ketika Kafka down, Relay Worker akan gagal mempublish. Namun event tetap tersimpan aman di tabel `outbox_messages` PostgreSQL dengan status "pending". Ketika Kafka kembali online, Relay Worker akan membaca ulang semua event yang pending dan mempublishnya. Saga akan melanjutkan dari titik berhenti.
>
> Dampaknya hanya pada **latensi**: Order tidak akan segera diproses karena event belum terkirim ke consumer. Namun **tidak ada data yang hilang** dan **tidak ada oversell** yang terjadi.

---

### Q14: *"Apakah ada masalah dengan solusi Redis Lua ini jika Redis di-scale menjadi Cluster?"*

**Jawaban:**

> Ya, dan ini adalah trade-off yang saya sadari. Lua Script di Redis tidak bisa dieksekusi jika key-nya tersebar di beberapa node berbeda (*cross-slot*). Lua Script kita menggunakan dua key sekaligus: `stock:{productID}` dan `reserve_idemp:{eventID}`.
>
> Solusinya adalah menggunakan **Redis Hash Tags** `{productID}`. Dengan format key menjadi `stock:{prod_1}` dan `reserve_idemp:{prod_1}:{eventID}`, Redis Cluster *menjamin* kedua key ditempatkan di shard yang sama, sehingga Lua Script tetap bisa berjalan atomik.
>
> Ini adalah item yang sudah saya catat sebagai *tech debt* jika sistem perlu di-upgrade ke Redis Cluster.

---

### Q15: *"Mengapa Anda menggunakan `FOR UPDATE SKIP LOCKED` di timeout worker?"*

**Jawaban:**

> `FOR UPDATE SKIP LOCKED` adalah pola PostgreSQL untuk **safe parallel processing** tanpa race condition.
>
> Timeout Worker bertugas mencari order yang sudah melewati batas waktu dan membatalkannya. Jika kita menjalankan 3 replika pod `order-service` secara bersamaan (untuk HA), ketiga pod tersebut akan menjalankan Timeout Worker masing-masing. Tanpa lock, ketiga pod bisa mengambil order yang sama dan memicunya untuk di-cancel tiga kali — walaupun idempotency akan mencegah efek buruk, ini pemborosan resource.
>
> Dengan `FOR UPDATE SKIP LOCKED`: setiap pod mengunci baris order yang sedang ia proses. Pod lain yang datang belakangan akan **melewati** (SKIP) baris yang sudah dikunci dan mengambil baris berikutnya. Hasilnya: beban kerja terdistribusi rata antar pod tanpa konflik. Ini adalah pola standar untuk *job queue* di Kubernetes.

---

## BAGIAN 5: Pertanyaan Reflektif

### Q16: *"Apa tantangan terbesar yang Anda hadapi saat membangun aplikasi ini?"*

**Jawaban:**

> Jujur, ada tiga tantangan yang paling menguras pikiran, dan masing-masing mengajarkan saya hal yang berbeda.
>
> ---
>
> **Tantangan #1 (Terberat): Stock Leak Edge Case — masalah yang tidak terlihat di permukaan.**
>
> Di awal, saya pikir Lua Script di Redis sudah cukup untuk menjamin atomicity. Masalahnya, Lua Script menjamin atomicity *di dalam Redis*, tapi tidak ada yang menjamin atomicity antara Redis dan PostgreSQL.
>
> Skenario yang saya temukan: Inventory Service berhasil menjalankan Lua Script (stok berkurang di Redis), tapi sebelum sempat menulis ke tabel `outbox_messages`, proses Go crash (misalnya karena OOM kill oleh kernel). Setelah restart, stok di Redis sudah terpotong permanen, tidak ada event di outbox, tidak ada order yang terbuat. Stok itu "menghilang".
>
> Ini bukan bug yang mudah direproduksi — hanya terjadi dalam race condition yang sangat spesifik. Saya akhirnya menyadarinya dengan cara membaca ulang kode dan mensimulasikan crash di tengah-tengah operasi.
>
> Solusinya adalah **Reconciliation Job**: sebuah background worker yang berjalan tiap 1 menit, membandingkan key `reserve_idemp:*` di Redis dengan tabel `outbox_messages` di PostgreSQL. Jika ada key yang sudah berumur lebih dari 5 menit tapi tidak ada entri di outbox, stok dikembalikan secara otomatis. Ini adalah implementasi nyata dari *Saga Compensating Transaction* yang tidak ada di buku teks, tapi sangat penting di produksi.
>
> ---
>
> **Tantangan #2: Distributed Tracing melalui Kafka — "lubang hitam" observability.**
>
> OpenTelemetry mudah dipasang di HTTP dan gRPC karena ada interceptor/middleware bawaan yang otomatis menginjeksi dan mengekstrak `traceparent` header. Tapi Kafka berbeda.
>
> Kafka record tidak punya "header HTTP". Saya harus secara manual:
> 1. Mengekstrak `TraceContext` dari `context.Context` saat *produce* ke Kafka.
> 2. Menginjeksikannya ke dalam `kafka.Message.Headers` (format key-value byte slice).
> 3. Di sisi consumer, membaca headers tersebut, me-reconstruct `SpanContext`, lalu membuat child span yang terhubung ke trace yang sama.
>
> Tanpa ini, setiap trace akan "putus" di Kafka — Jaeger hanya akan menampilkan setengah perjalanan transaksi. Butuh waktu satu hari penuh untuk memahami API `otel/propagation` dan membuatnya bekerja end-to-end dari API Gateway hingga Payment Service dalam satu trace yang utuh.
>
> ---
>
> **Tantangan #3: Membuat Redis Sentinel transparan terhadap kode Go yang sudah ada.**
>
> Ketika memutuskan untuk menambahkan Redis Sentinel di environment production, tantangannya adalah: kode Go di semua service sudah menggunakan `redis.NewClient()` (Standalone). Mengganti semua pemanggilan ini ke `redis.NewFailoverClient()` secara hardcode akan membuat kode development dan production berbeda, dan rawan error.
>
> Solusi yang saya terapkan adalah **Factory Pattern berbasis environment variable**: satu fungsi `NewRedisClient()` yang membaca `REDIS_MODE` dari ENV. Jika `REDIS_MODE=sentinel`, ia mengembalikan `FailoverClient`. Jika tidak, ia mengembalikan `Standalone Client`. Semua service menggunakan fungsi factory ini, sehingga tidak ada satu baris pun kode bisnis yang berubah antara environment dev dan prod. Konfigurasinya sepenuhnya diatur dari `docker-compose.yml` vs `docker-compose.prod.yml`.
>
> ---
>
> **Pelajaran terbesar:** Tantangan terberat bukan soal menulis kode yang *berjalan*, tapi menulis kode yang *benar* dalam kondisi partial failure. Distributed systems tidak gagal secara total — mereka gagal *sebagian*, dan justru di sanalah bug yang paling berbahaya bersembunyi.

---

*Dokumen ini mencakup pertanyaan yang paling umum. Jika ada pertanyaan lain yang Anda temui di interview dan belum tercakup di sini, konsultasikan dan akan saya tambahkan.*
