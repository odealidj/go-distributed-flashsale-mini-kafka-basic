# Tutorial Membaca Hasil K6 (Studi Kasus: Flash Sale)

Dokumen ini adalah panduan sederhana untuk membaca dan menganalisis hasil output K6. Kita akan menggunakan hasil tes nyata dari sistem **Flash Sale (Raw Polling)** sebagai bahan belajar.

## 1. Membedah Blok Output K6

Berikut adalah hasil contoh dari pengujian K6 kita:

```text
  █ THRESHOLDS 

    http_req_duration
    ✓ 'p(95)<1500' p(95)=6.36ms

  █ TOTAL RESULTS 

    checks_total.......: 66447   1648.713376/s
    checks_succeeded...: 100.00% 66447 out of 66447
    checks_failed......: 0.00%   0 out of 66447

    HTTP
    http_req_duration..............: avg=3.47ms   min=1.35ms   med=3.09ms   max=112.24ms p(90)=5.23ms   p(95)=6.36ms  
      { expected_response:true }...: avg=4.99ms   min=1.93ms   med=3.47ms   max=112.24ms p(90)=5.82ms   p(95)=10.39ms 
    http_req_failed................: 99.46% 66195 out of 66548
    http_reqs......................: 66548  1651.219434/s

    EXECUTION
    iteration_duration.............: avg=105.85ms min=101.72ms med=103.85ms max=1.62s    p(90)=106.11ms p(95)=107.29ms
    iterations.....................: 66213  1642.907261/s
    vus............................: 14     min=14             max=200
    vus_max........................: 200    min=200            max=200
```

Mari kita kupas rahasia di balik angka-angka tersebut satu per satu.

---

## 2. Kenapa HTTP Request Failed Hampir 100%?

```text
http_req_failed................: 99.46% 66195 out of 66548
http_reqs......................: 66548
```

**Pertanyaan:** "Waduh, failed 99%? Apakah server saya rusak/down?"
**Jawaban:** Tidak! Justru ini adalah bukti sistem *Flash Sale* Anda beroperasi dengan sempurna.

**Penjelasan Sederhana:**
1. K6 secara bawaan (default) akan menganggap bahwa HTTP status **400 ke atas** (seperti 404 Not Found, 429 Rate Limit, 409 Conflict, atau 500 Internal Error) adalah request yang "Gagal" (Failed).
2. Di sistem *Flash Sale* kita, stok barang hanya ada **100**. 
3. Dari total **66.548** tembakan *request*, **66.195** di antaranya ditolak oleh server dengan pesan HTTP `409 Conflict` (Stok Habis). Inilah yang menyumbang persentase *failed* sebesar 99.46%.
4. Jika kita mengurangi total request dengan request yang failed (66.548 - 66.195), tersisa **353 request yang sukses** (berstatus HTTP 2xx).
5. Dari mana datangnya angka 353?
   - **100 request** adalah aksi *Checkout* (berebut stok) yang sukses di detik-detik pertama.
   - **~153 request** adalah aksi API Gateway me-*refresh*/mengecek status pesanan tersebut (*GetOrder* Polling).
   - **100 request** adalah aksi *Payment* (Membayar tagihan).
   - (*Total pas: ~353 request*).

**Pelajaran:** Selalu ingat konteks bisnis aplikasi Anda. Pada *Flash Sale*, ditolaknya 99% request karena kehabisan stok adalah hal yang sangat normal dan justru diharapkan! Jangan sampai sistem malah menerima 60.000 pesanan padahal stok cuma 100 (itu namanya *overselling*).

---

## 3. Menghitung Kecepatan Sistem Anda (Latensi)

```text
http_req_duration..............: avg=3.47ms  p(95)=6.36ms  max=112.24ms
  { expected_response:true }...: avg=4.99ms  p(95)=10.39ms max=112.24ms
```

**Pertanyaan:** "Berapa lama rata-rata user harus menunggu server merespon?"
**Jawaban:** Tergantung, apakah dia user yang kehabisan stok, atau user yang kebagian barang.

**Penjelasan Sederhana:**
- `avg` (Average): Waktu respon rata-rata.
- `p(95)` (Percentile 95): Artinya, **95% dari seluruh request diselesaikan lebih cepat dari waktu ini**. Hanya 5% sisa request apes yang lebih lambat dari ini. Ini adalah standar emas industri ketimbang rata-rata (`avg`).

Di laporan di atas, ada 2 jenis `http_req_duration`:
1. **http_req_duration (Keseluruhan):** Termasuk request yang sukses maupun yang *failed* (409 Stok Habis). Karena 99% adalah request 409, maka ini menggambarkan kecepatan server menolak *request*. Server menolak request hanya dalam waktu **3.47ms (rata-rata)**. Hal ini sangat cepat karena kita mengecek stok langsung di Memori Cache (Redis), tanpa menyentuh Database (Postgres) atau Kafka.
2. **expected_response: true:** Ini adalah kecepatan khusus untuk request yang "Sukses" (yang mendapat barang). Kecepatannya sedikit lebih lambat, yakni **4.99ms (rata-rata)** dan **10.39ms (P95)**. Hal ini wajar karena request yang sukses harus memproses logika antrean, memotong stok, dan meneruskan data ke Kafka.

**Pelajaran:** Bandingkan selalu `p(95)` alih-alih `avg`. Jika Anda memiliki `avg` 10ms namun `p(95)` 5000ms, berarti sistem Anda tersendat (ngadat) pada saat-saat tertentu meskipun rata-ratanya cepat.

---

## 4. Kecepatan Tembak (RPS) vs Waktu Tidur (Iteration)

```text
http_reqs......................: 66548  1651.21/s
iteration_duration.............: avg=105.85ms
vus_max........................: 200
```

**Pertanyaan:** "Kenapa RPS (Request per Second) saya tertahan di kisaran 1650/s? Apa server saya ngos-ngosan (mencapai batas limit)?"
**Jawaban:** Tidak! Limit tersebut bukan berasal dari performa backend, melainkan berasal dari limit *skrip K6* itu sendiri.

**Penjelasan Sederhana:**
- `vus_max` adalah *Virtual Users* (jumlah agen k6 yang menyerang server secara bersamaan, layaknya tab browser terbuka). Anda memakai 200 agen.
- `iteration_duration` adalah waktu total yang dibutuhkan 1 agen untuk menyelesaikan 1 skenario (1 kali tembak).
- Rata-ratanya **105.85 milidetik**. Kenapa selama itu padahal respon server cuma 3ms? 
- Karena di dalam *source code* skrip K6 kita, ada baris:
  ```javascript
  if (checkoutRes.status !== 200) {
      sleep(0.1); // Tidur 100 milidetik (0.1 detik)
      return;
  }
  ```
- Waktu Iterasi = Response Server (3.4ms) + Sleep K6 (100ms) + Overhead jaringan (~2.4ms) = **~105.8ms**.

Secara Matematika Murni:
Jika 1 agen butuh 0.105 detik untuk 1 tembakan, maka 1 agen bisa menembak `1 / 0.105 = 9.5` kali dalam satu detik.
Karena Anda punya 200 agen, maka total peluru maksimal yang bisa ditembakkan adalah `200 x 9.5 = 1900 RPS`.
Angka aktual **1651 RPS** tercapai mengingat di awal (5 detik pertama) dan akhir tes agen tidak langsung 200 (melainkan naik-turun secara perlahan sesuai skenario *Ramping VUs*).

**Pelajaran:** Jika Anda ingin melihat Server Backend (Golang) Anda benar-benar meledak kelelahan, Anda harus mengubah nilai `sleep(0.1)` di skrip K6 menjadi `sleep(0)`, atau meningkatkan jumlah VUs ke angka yang sangat ekstrem (misal 5000).

---

## 5. Checks Total vs HTTP Reqs

```text
checks_total.......: 66447   1648.71/s
http_reqs..........: 66548   1651.21/s
```

**Pertanyaan:** "Apa bedanya checks dengan http_reqs?"
**Jawaban:**
- `http_reqs` adalah jumlah *network request* sungguhan yang terbang dari komputer Anda ke server.
- `checks` adalah kondisi/assert (mirip *Unit Test* `expect()`) di dalam kode skrip K6.
  
Pada tes ini, 1 HTTP request hanya dicek oleh 1 `check()`. Maka jumlahnya nyaris sama. Jika sebuah blok kode memiliki banyak syarat cek:
```javascript
check(res, {
  'status is 200': (r) => r.status === 200,
  'body has success': (r) => r.body.includes('success'),
});
```
Maka 1 *HTTP request* akan menghasilkan 2 buah *checks*. Ini sangat penting diperhatikan karena jumlah RPS `checks/s` yang tertera bisa jauh lebih tinggi daripada RPS `http_reqs/s`.

---

## 6. Menganalisis Pola Polling (Kasus Long Polling)

Pada skenario **Long Polling**, Anda mungkin mendapati hasil `http_req_duration` yang terlihat jomplang seperti ini:

```text
http_req_duration..............: avg=4.92ms    p(95)=6.72ms  
  { expected_response:true }...: avg=443.49ms  p(95)=1.01s   max=1.01s
```

**Pertanyaan:** "Kenapa request yang sukses (`expected_response:true`) butuh waktu sampai **1.01 detik**? Apakah Kafka atau Postgres saya selambat itu?"
**Jawaban:** Tidak! Kecepatan Kafka Anda mungkin jauh lebih cepat dari itu, namun skema `Long Polling` kita sendiri yang memaksa waktu tunggunya menjadi kelipatan **500ms**.

**Penjelasan Sederhana:**
1. Di dalam *source code* API Gateway (Golang) khusus untuk *Long Polling*, kita menggunakan teknik perulangan berkalang (*looping*) untuk mengecek status pesanan ke Postgres secara berkala.
2. Jika pesanan masih `PENDING`, API Gateway akan memanggil `time.Sleep(500 * time.Millisecond)` sebelum mengecek kembali.
3. Ini berarti, respons sukses (*SUCCESS*) hanya bisa dikembalikan pada interval **~0ms** (jika pekerja selesai sebelum putaran pertama), **~500ms** (putaran kedua), atau **~1000ms / 1 detik** (putaran ketiga).
4. Angka `p(95)=1.01s` adalah bukti nyata bahwa 95% *request* yang berhasil sukses mendapatkan barang harus menunggu hingga Gateway mengecek pada putaran detik ke-1!
5. Angka `avg=443.49ms` berarti banyak *request* yang beruntung dan selesai pada putaran pertama (~0ms) atau putaran kedua (~500ms).

**Pelajaran:** *Long Polling* adalah teknik lawas yang rakus daya (*CPU/Memory Intensive*) karena server harus terus-terusan terbangun dan tertidur untuk mengecek Database. Jeda waktu tunggu (*sleep*) yang Anda berikan di kode akan sangat memanipulasi metrik latensi pada pengujian K6 Anda.

---

## 7. Menganalisis Keunggulan Real-Time (Kasus PubSub)

Ini adalah bukti nyata betapa berharganya proses *Load Testing* yang dibarengi dengan analisis yang tepat. Setelah kita memberantas *bug* arsitektur (anti-pattern) pada sesi sebelumnya, mari kita amati hasil perbaikannya:

```text
http_reqs......................: 66591  1652.60/s
http_req_duration..............: avg=4.65ms    p(95)=6.59ms  
  { expected_response:true }...: avg=363.91ms  p(95)=934.67ms 
```

**Pertanyaan:** "Lho, bukannya latensi `expected_response` (request sukses) pada PubSub ini mirip-mirip dengan Long Polling (~1 detik)? Bedanya di mana?"
**Jawaban:** Angka maksimalnya mungkin terlihat mirip, namun **asal mula terjadinya angka tersebut berbeda 180 derajat!**

**Penjelasan Sederhana:**
1. **Kecepatan RPS Telah Pulih:** Sebelumnya PubSub kita ngos-ngosan di angka 870 RPS. Setelah kita memindahkan logika `Subscribe` ke belakang pengecekan stok, ia kini berlari di **~1652 RPS** (kembali setara dengan yang lain) dan `p(95)` sukses ditekan ke angka **6.59ms**. Arsitektur *gateway* kita sudah bersih dari pemborosan koneksi.
2. **Latensi Murni (Tanpa Jeda Buatan):** Pada *Long Polling*, respons tertahan hingga 1 detik karena kita memaksa kode untuk `sleep(500ms)`. Pada *PubSub*, **TIDAK ADA `sleep`**. Respons akan langsung dikirimkan ke pengguna di milidetik yang sama begitu sinyal dari *Redis PubSub* diterima.
3. **Mengapa Masih Mencapai 900ms?** Bayangkan 100 pengguna menyerbu pintu kasir secara serempak di detik yang sama. Kasir (Kafka *Consumer*) harus memproses mereka satu per satu. Pengguna urutan ke-1 mungkin selesai diproses dalam 10ms. Namun pengguna urutan ke-100 harus menunggu hingga 99 orang di depannya selesai dilayani. 
Waktu tunggu **934.67ms** ini adalah **latensi nyata dari antrean Kafka dan pemrosesan Database Postgres** kita, bukan hasil dari waktu tidur buatan!

**Pelajaran:** Inilah kekuatan sejati arsitektur *Event-Driven* (*PubSub*). Kita mendapatkan latensi murni (*Real-time* sesungguhnya) sesuai batas kecepatan perangkat keras dan *database* kita, tanpa membuang-buang siklus CPU *gateway* untuk terus-menerus mengecek (polling) data yang belum berubah. 

---

## 8. Menganalisis Pola Streaming (Kasus Server-Sent Events / SSE)

Terakhir, mari kita bedah skenario **SSE** yang memiliki karakteristik unik namun menorehkan angka latensi yang familiar:

```text
http_req_duration..............: avg=5.18ms    p(95)=7.79ms  
  { expected_response:true }...: avg=502.43ms  p(95)=1.01s   max=1.01s 
```

**Pertanyaan:** "Lho, bukannya SSE itu *streaming* yang seharusnya canggih dan *real-time* seperti *PubSub*? Kenapa hasil `expected_response`-nya sama persis kelambatannya dengan *Long Polling* (1 detik)?"
**Jawaban:** Ya, SSE adalah teknologi koneksi terbuka (*streaming*), namun **implementasi *backend* kita (di dalam Go)** pada saat melayani *stream* tersebut meminjam teknik *polling* ke *database*.

**Penjelasan Sederhana:**
1. Jika Anda membedah kode sumber `api-gateway` pada `POST /api/v1/checkout/sse`, Anda akan menemukan blok kode ini:
   ```go
   // send keepalive
   fmt.Fprintf(w, ": keepalive\n\n")
   flusher.Flush()
   time.Sleep(1 * time.Second)
   ```
2. Ya! Meskipun koneksi jaringannya dibiarkan terbuka (mirip selang air), ternyata air yang dialirkan (pengecekan ke Postgres) hanya di-*pompa* setiap **1 detik sekali**.
3. Itulah sebabnya mengapa batas maksimal `p(95)` dan `max` tidak pernah melewati batas ~1.01 detik. Sekalipun Kafka sudah selesai memproses pesanan di detik ke-0.2, server menahannya dan baru mengecek ulang pada ketukan (detak) 1 detik.
4. Ini menjadikan metrik *SSE* kita pada pengujian K6 ini sangat mirip (bahkan sedikit lebih lambat) daripada metrik *Long Polling* yang di-*set* tidur selama 500ms.

**Pelajaran:** Teknologi protokol komunikasi (seperti SSE, WebSockets, atau HTTP biasa) hanyalah sekadar jalan atau "pipa". Sekalipun Anda menggunakan pipa emas (WebSockets/SSE), apabila cara Anda mengambil air dari sumur (Database) menggunakan gayung bocor (teknik *polling + sleep* lambat), maka hasilnya ke pelanggan tetap akan lambat. Ini membuktikan mengapa kombinasi **[SSE + PubSub]** (Meneruskan *Event* Redis ke Stream SSE) adalah standar emas industri ketimbang **[SSE + Polling Database]**.

---

## 9. Hasil Ekstrem (Beban 500 VUs & Real-time SSE)

Setelah Anda membedah kode dan mengubah logika SSE agar **berlangganan langsung ke Redis PubSub** (tidak lagi melakukan *sleep* 1 detik), ditambah dengan melipatgandakan jumlah agen penyerang menjadi **500 VUs** dan stok **200**, Anda akan mendapatkan hasil seperti ini:

```text
http_reqs......................: 155277 3853.29/s
http_req_duration..............: avg=11.9ms    p(95)=25.84ms 
  { expected_response:true }...: avg=846.62ms  med=86.02ms  max=2.93s  p(95)=2.85s   
```

**Analisis Kemajuan Sistem:**
1. **Ledakan RPS:** Kapasitas tembak K6 meroket tajam menjadi **3.853 RPS** (lebih dari 150.000 *request* terproses). API Gateway berhasil menangani lonjakan dari 500 pengguna secara bersamaan tanpa satupun *request* yang *Error 500*.
2. **Penolakan Super Cepat:** Metrik `p(95)` kecepatan tolaknya tetap sangat cepat di **25.84ms** meskipun diserang setengah ribu agen.
3. **Murni Real-time:** Anda akan melihat `max` dan `p(95)` kini berada di angka fleksibel (seperti 2.93s atau 2.85s) dan **bukan lagi kelipatan 1 detik**. Artinya kode SSE telah bersih dari blokade `sleep`!
4. **Hukum Antrean:** Angka batas atas 2.85 detik ini murni terjadi karena Kafka kini disuruh memproses 200 pemenang (*checkout* yang sukses). Ekor antrean (orang ke-199 dan ke-200) wajar harus menunggu lebih lama agar 198 orderan di depan mereka selesai dicatat di Database. Begitu selesai, SSE langsung menyemburkannya seketika (0 ms jeda buatan). Hal ini dibuktikan dari angka median `med=86.02ms` yang berarti separuh orang pertama selesai dilayani di bawah sepersepuluh detik!

**Pelajaran:** Implementasi **[SSE + PubSub]** (Meneruskan *Event* Redis langsung ke *Stream* SSE) adalah standar emas yang digunakan oleh raksasa industri. Sistem Anda kini sangat efisien, murni *Real-time*, dan siap diterjunkan ke *Production* untuk perang *Flash Sale* yang sesungguhnya!

---

## 10. Uji Keamanan Idempotensi (Mencegah Double-Checkout)

Selain kecepatan (*Load Testing*), K6 juga kita gunakan untuk menguji keamanan *Microservices* lewat skrip `02_idempotency_test.js`. Skrip ini mensimulasikan skenario di mana pelanggan nakal (atau karena *lag* jaringan) mengklik tombol beli 3x berturut-turut dalam sepersekian detik.

Jika tes ini berjalan mulus, Anda akan melihat metrik kustom (buatan kita) muncul di hasil akhir:

```text
  █ THRESHOLDS 
    double_checkout_prevented
    ✓ 'count>0' count=400

    idempotency_failures
    ✓ 'count==0' count=0

  █ TOTAL RESULTS 
    CUSTOM
    double_checkout_prevented......: 400    
    idempotency_failures...........: 0      

    http_req_failed................: 66.66% 400 out of 600
```

**Cara Membaca Metrik Keamanan Ini:**
1. **`double_checkout_prevented: 400`**: Ini adalah jumlah peluru (request duplikat) yang berhasil ditangkis oleh API Gateway. Dari 200 *Virtual Users* yang masing-masing menembakkan 3 *request* (Total 600 klik), klik pertama berhasil diproses (200 sukses), dan 400 klik sisanya sukses dicegat dan dibuang!
2. **`idempotency_failures: 0`**: Ini adalah indikator lolos/tidaknya sistem Anda. Angka 0 berarti sistem Database / Kafka sama sekali tidak pernah kebobolan mencatat pesanan yang sama sebanyak dua kali (*Double-Spend*). Jika angka ini lebih dari 0, artinya ada uang/stok yang bocor!
3. **`http_req_failed: 66.66%`**: Pada tes normal, angka gagal 66% adalah bencana. Namun pada tes idempotensi, angka 66.66% (400 dari 600) adalah **target kesuksesan mutlak**! Ini membuktikan bahwa 2/3 dari klik membabi buta pengguna telah sengaja diblokir oleh sistem kita dengan status `HTTP 409 Conflict (Idempotency Error)`.

**Tips Uji Coba Terisolasi:**
Karena sistem pelindung API Gateway kita bekerja dengan cara mencatat dan mengingat *Idempotency-Key* di Redis, Anda tidak bisa menggunakan kunci yang sama untuk tes yang berulang kali, karena Redis akan menolak klik pertama Anda dan mengiranya sebagai *spam* dari tes sebelumnya. Pastikan skrip K6 Anda menempelkan ID unik (*Timestamp*) pada setiap pengujian.

---

## 11. Uji Keselamatan Stok (No-Oversell / Golden Test)

Pengujian `04_no_oversell.js` adalah inti (nyawa) dari sebuah sistem *Flash Sale*. Skrip ini dirancang khusus untuk membuktikan satu hal secara matematis: **"Apakah mungkin stok barang menjadi minus jika diserang ribuan pembeli secara bersamaan?"**

Berbeda dengan skrip lain yang menghasilkan tabel metrik standar, skrip ini akan mencetak laporan ASCII khusus (Custom Report) seperti ini:

```text
╔══════════════════════════════════════════════════════════╗
║           NO-OVERSELL TEST - HASIL VERIFIKASI           ║
╠══════════════════════════════════════════════════════════╣
║  Stok Awal      :   200                                  ║
║  Total User     :  5000                                  ║
║  Total Request  :  5000                                  ║
╠══════════════════════════════════════════════════════════╣
║  ✅ 202 Accepted:   200 (checkout berhasil)              ║
║  ⚠️  409/429    :  4800 (stok habis / rate limited)      ║
║  ❌ Error Sistem:     0                                  ║
╠══════════════════════════════════════════════════════════╣
║  ✅ TIDAK ADA OVERSELL                                   ║
╚══════════════════════════════════════════════════════════╝
```

**Cara Membaca Custom Report Ini:**
1. **Laporan Khusus (Bukan Bawaan K6):** Laporan cantik ini sengaja diprogram di dalam fungsi `handleSummary()` K6. Alasan utamanya adalah karena K6 standar tidak bisa melakukan pengecekan perbandingan variabel (misalnya: `Total Sukses <= Stok Awal`) untuk meneriakkan peringatan aman atau bahaya.
2. **`202 Accepted: 200`**: Angka inilah piala kemenangan Anda. Dari 5000 pengguna yang menyerbu dalam satu milidetik yang sama, skrip LUA di Redis secara konsisten dan atomik hanya mengizinkan tepat 200 pengguna (sesuai stok awal) untuk masuk dan membuat pesanan.
3. **`409/429: 4800`**: Sebanyak 4800 pembeli yang kalah cepat otomatis ditolak oleh sistem tanpa menghancurkan *database* (karena penolakan dilakukan di pintu gerbang Gateway/Redis, bukan di Postgres). 
4. **`Error Sistem: 0`**: Menandakan tidak ada interupsi atau *Crash* pada layanan (misal: *timeout*, koneksi terputus, atau *Panic* di Golang) di tengah badai *Request*.
5. **Indikator Keberhasilan Mutlak (`TIDAK ADA OVERSELL`)**: Jika Anda melihat centang hijau ini, artinya *Logic* pemotongan stok berkecepatan tinggi Anda sudah siap dihadapkan pada skenario nyata setingkat *E-Commerce* raksasa!

---

## 12. Uji Integrasi Kompensasi Saga (Saga Compensation Test)

Pengujian `05_compensation_test.js` memiliki karakteristik yang sangat berbeda dibandingkan kelima skrip sebelumnya. Jika skrip lainnya adalah murni *Load Testing* (Uji Beban) untuk mencari kelemahan performa, skrip ini adalah sebuah **End-to-End (E2E) Functional Test**.

Tujuan utamanya adalah memvalidasi kebenaran logika (*logic correctness*) dari *Distributed Transaction* menggunakan *Saga Pattern*.

Ketika skrip ini dijalankan, Anda akan melihat rentetan hasil hijau seperti ini:

```text
    ✓ Checkout direspon 202 Accepted
    ✓ Respon memiliki event_id
    ✓ Order ditemukan
    ✓ Status order PENDING
    ✓ Pembayaran ditolak gateway (code=Unknown)
    ✓ Status order berubah menjadi CANCELLED
```

**Cara Membaca Metrik Fungsional Ini:**

1. **Kenapa hanya 1 VU?** Anda mungkin menyadari bahwa `vus: 1` sengaja dikunci untuk skrip ini. Alasannya karena kita menguji alur asinkron di dalam Kafka yang saling bergantung satu sama lain secara berurutan. Mengirim beban masif secara konkuren akan memicu NGINX *Rate Limiter* (`429 Too Many Requests`), yang akan mengotori hasil tes fungsional murni ini.

2. **Langkah 1 & 2 (Fase Checkout)**: K6 bertindak sebagai *user* yang menekan tombol Beli. K6 mengecek apakah API Gateway merespons dengan `202 Accepted` dan mengembalikan `event_id` (Idempotency Key). Ini membuktikan gerbang depan sistem bisa menerima pesanan.

3. **Langkah 3 & 4 (Fase Verifikasi PENDING)**: K6 secara otomatis "menunggu" dan secara berkala menembak API `GET /api/v1/orders/{event_id}`. K6 memvalidasi apakah *Order Service* benar-benar berhasil membuat pesanan di *PostgreSQL* dengan status `PENDING` hasil dari mendengarkan *event* Kafka.

4. **Langkah 5 (Memancing Kegagalan)**: K6 sengaja mengirim instruksi pembayaran senilai `150004` (dengan akhiran angka 4). API *Payment Service* diprogram untuk menolak angka ini, sehingga ia memicu *event* `PaymentFailed` ke Kafka. Cek ini memastikan Gateway sukses menolak pembayaran.

5. **Langkah 6 (Pembuktian Kompensasi / Rollback)**: Ini adalah momen pembuktian! K6 menunggu beberapa detik, lalu memanggil kembali data Order. K6 mendeteksi bahwa pesanan secara ajaib (tanpa disuruh K6) telah berubah wujud menjadi `CANCELLED`. Ini membuktikan bahwa mekanisme *rollback* (*Compensation*) bekerja dengan sempurna, dan stok barang di Redis yang tadinya terpotong pasti telah dikembalikan (`RefundStock`).

Jika semua tes ini mencetak tanda *Checklist* Hijau, Anda bisa tidur nyenyak karena tidak akan ada kasus **"Barang habis, tapi tidak ada yang bayar"** di *Flash Sale* Anda!

---

## 13. Uji Ketahanan Jangka Panjang (Soak Test)

Pengujian `03_soak_test.js` dan versi pemanasannya `03_soak_test_3m.js` adalah pengujian khusus yang berfokus pada **stabilitas jangka panjang** (durasinya bisa berjam-jam di dunia nyata). Tujuannya bukan untuk melihat seberapa cepat sistem Anda hari ini, tetapi apakah sistem Anda **masih secepat hari ini dalam 30 menit ke depan** di bawah tekanan konstan.

Ini berguna untuk mendeteksi penyakit kronis pada sistem yang baru terlihat seiring berjalannya waktu:
- *Memory Leak* (Goroutine yang menggantung dan menghabiskan RAM secara perlahan).
- *Connection Pool Exhaustion* (Aplikasi lupa menutup koneksi ke PostgreSQL / Redis sehingga habis).
- Peningkatan latensi yang merayap naik karena antrean data.

Saat Anda menjalankan tes ini (misal versi 3 menit), K6 mencetak metrik yang mensimulasikan gabungan aktivitas nyata:
- 70% membaca katalog (`GET /products` yang memukul PostgreSQL).
- 30% berbelanja (`POST /checkout` yang memukul Redis dan Kafka).

**Cara Membaca Metrik Soak Test:**

```text
    ✓ Product list status valid
    ✓ Checkout status valid

    system_errors..................: 0.00%   ✓ 0          ✗ 30494
  ✓ soak_checkout_latency_ms.......: avg=2.71ms  min=1ms      med=2ms      max=68ms    p(95)=3ms
  ✓ soak_product_list_latency_ms...: avg=1.17ms  min=0s       med=1ms      max=54ms    p(95)=2ms
```

1. **`system_errors` (Kegagalan Sistem)**: Anda harus melihat ini berada di angka **`0.00%`**. Jika ada angka kegagalan yang merayap naik di tengah jalan (misalnya 1%, 5%, lalu 20%), ini pertanda kuat adanya *Connection Pool Exhaustion* di mana Golang tidak bisa lagi membuat koneksi baru ke *Database*.
2. **`soak_checkout_latency_ms` & `soak_product_list_latency_ms`**: K6 membuatkan metrik kustom (Trend) untuk memisahkan kecepatan "Beli" dan "Baca". Cek angka `p(95)` (Persentil 95). Jika angkanya **konsisten sangat kecil** (seperti contoh di atas: `2-3ms`) sejak menit pertama hingga menit terakhir, berarti *Garbage Collector* Golang dan kinerja *Database* berjalan luar biasa stabil.
3. **`http_req_failed`**: Dalam tes ini, kegagalan `http_req_failed` (seperti `429 Rate Limit` atau `409 Conflict` karena stok habis) adalah **Normal dan Disengaja**. K6 membedakan antara penolakan sistematis (409/429) dengan kegagalan sistem (*Crash/Timeout*). Selama status *valid* (centang hijau di awal) terpenuhi, respons `429` justru menunjukkan bahwa tameng NGINX Anda bekerja sempurna dari *spam*.
4. **Kalibrasi Stok Kustom (`seed-stock-soak`)**: Jika Anda ingin menguji batas maksimal pipa antrean Kafka (bukan sekadar NGINX), pastikan stok produk tidak dibiarkan default 200, karena Kafka akan menganggur setelah stok habis. Pada tes ini, perintah `make test-soak` otomatis menyuntikkan **1.000.000 stok** agar jalur Kafka ➞ Order Service terus disiksa selama 30 menit tanpa henti.

Jika Anda melewati batas waktu pengujian (baik 3m maupun 30m) tanpa ada grafik latensi yang menanjak naik, selamat, layanan Anda telah "Kebal" (*Bulletproof*)!

---

## 14. Menganalisis Perlindungan Gerbang (Nginx Rate Limiter)

Jika Anda menjalankan `make test-load-nginx` yang khusus membidik batas perlindungan Nginx, Anda akan mendapatkan metrik yang sangat menarik:

```text
  █ TOTAL RESULTS 

    checks_total.......: 340628  
    checks_succeeded...: 100.00% 340628 out of 340628
    
    HTTP
    http_req_duration..............: avg=2.11ms   p(95)=4.79ms  
      { expected_response:true }...: avg=478.93ms p(95)=972.73ms
    http_req_failed................: 99.85% 170184 out of 170429
    http_reqs......................: 170429 4219.27/s
```

**Cara Membaca Metrik Perlindungan Nginx Ini:**

1. **Kenapa `http_req_failed` 99.85%?** K6 menganggap HTTP `429 Too Many Requests` sebagai kegagalan (*failed*). Namun dalam konteks ini, **ini adalah kesuksesan mutlak!** Angka 99.85% membuktikan bahwa Nginx berhasil memotong paksa trafik spam (Thundering Herd) dan menyelamatkan *backend* Go Anda dari kematian.
2. **Kenapa rata-rata latensinya sangat cepat (2.11ms)?** Karena Nginx menolak (drop) koneksi berlebih tersebut langsung di pintu gerbang dalam hitungan 1-4 milidetik tanpa repot-repot meneruskannya ke Golang atau Postgres. Sangat hemat CPU/RAM!
3. **Lalu apa itu `{ expected_response:true }` yang 478ms?** Ini mewakili segelintir *request* "beruntung" (0.15%) yang diizinkan masuk oleh Nginx. Request ini benar-benar diteruskan ke Microservices Go dan diproses melewati Kafka Saga. Waktu ~478ms ini adalah latensi asli sistem asinkron kita.
4. **`checks_succeeded: 100%`**: Artinya meskipun 99.85% ditolak, penolakannya sesuai prosedur (HTTP 429), dan sisanya diproses dengan benar. **Tidak ada satupun server yang crash (HTTP 5xx).** Sistem menolak trafik secara elegan!

---

## Tips Tambahan: Menerapkan Perubahan Kode ke K6

Ketika Anda menganalisis hasil K6 dan menyadari adanya *bottleneck* (kemacetan) pada kode Golang Anda, Anda pasti akan mengedit *source code* tersebut (seperti yang kita lakukan pada kasus PubSub dan SSE di atas).

Satu hal yang **sangat penting** untuk diingat: Karena Anda menjalankan *microservices* menggunakan Podman/Docker, perubahan pada kode *Golang* Anda **tidak akan langsung terbaca** oleh skrip K6. Anda wajib menyuruh sistem untuk "memakan" kode baru tersebut dengan cara melakukan *build* ulang *container*-nya.

Gunakan perintah sakti ini setiap kali Anda selesai mengubah kode *backend* dan ingin mengujinya lagi dengan K6:
```bash
docker-compose --profile app up -d --build
```
*Perintah ini akan secara cerdas merakit ulang (build) hanya aplikasi Golang yang source code-nya berubah, dan langsung me-restart layanannya dalam hitungan detik tanpa mematikan Redis atau Kafka.*

---
*Semoga panduan singkat ini dapat membantu Anda "membaca pikiran" mesin saat melakukan proses Performance Engineering dan Load Testing!*
