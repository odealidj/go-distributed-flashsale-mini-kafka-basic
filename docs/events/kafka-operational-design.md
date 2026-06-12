# Kafka Operational Design

Dokumen ini menjelaskan konfigurasi operasional Kafka dalam sistem Flash Sale — dari topologi topic, partition key, consumer group, hingga mekanisme DLQ — agar sistem mampu menangani **10.000 RPS** tanpa menjadi bottleneck.

> [!NOTE]
> Kafka berjalan dalam mode **KRaft** (tanpa Zookeeper) menggunakan `bitnamilegacy/kafka:3.5.1`. Auto-create topic diaktifkan (`KAFKA_CFG_AUTO_CREATE_TOPICS_ENABLE=true`) untuk kemudahan development.

---

## 1. Daftar Topic

| Topic | Producer | Consumer | Fungsi |
|---|---|---|---|
| `flashsale.inventory.events` | Inventory Service (Relay Worker) | Order Service | Beri tahu Order Service bahwa stok berhasil di-reserve |
| `flashsale.payment.events` | Payment Service (Relay Worker) | Order Service | Beri tahu Order Service hasil pembayaran (sukses/gagal) |
| `flashsale.order.events` | Order Service (Relay Worker) | Inventory Service | Kirim `OrderCancelledEvent` untuk trigger saga compensation |
| `flashsale.order.dlq` | Order Service Consumer | *(manual replay)* | Dead Letter Queue — event gagal diproses oleh Order Service |
| `flashsale.inventory.dlq` | Inventory Service Consumer | *(manual replay)* | Dead Letter Queue — event gagal diproses oleh Inventory Service |

---

## 2. Partition Strategy

Partisi adalah kunci **konkurensi** di Kafka. Jika topic `flashsale.inventory.events` hanya punya 1 partisi, Order Service hanya bisa memproses dengan 1 thread secara bersamaan.

```
Topic: flashsale.inventory.events
├── Partition 0 → Consumer Instance A (order-service pod 0)
├── Partition 1 → Consumer Instance B (order-service pod 1)
├── ...
└── Partition 9 → Consumer Instance J (order-service pod 9)
```

**Rekomendasi:** Minimal **10 partisi** per topic utama untuk mendukung scale-out horizontal hingga 10 consumer instance.

### Partition Key yang Digunakan

| Event | Partition Key | Alasan |
|---|---|---|
| `StockReservedEvent` | `AggregateID` (= UUID `event_id`) | Menjamin event ini disebar rata, tetapi tetap urut per transaksi pesanan. |
| `PaymentCompletedEvent` | `AggregateID` (= UUID `order_id`) | Menjamin event masuk ke partisi yang sama dengan event sebelumnya untuk pesanan ini. |
| `OrderCancelledEvent` | `AggregateID` (= UUID `order_id`) | Menjamin pembatalan tidak mendahului pemrosesan event sebelumnya. |

> [!NOTE]
> *Update:* Relay Worker saat ini sudah menggunakan `AggregateID` (`event_id` atau `order_id`) sebagai Kafka Key. Hal ini memastikan beban terdistribusi merata (*load balancing* yang optimal) ke semua partisi sekaligus mempertahankan jaminan urutan (*ordering guarantee*) secara ketat untuk satu siklus pesanan.

---

## 3. Consumer Group Design

Setiap service consumer mendaftarkan diri dengan `group_id` yang unik dan konstan. Kafka mendistribusikan partisi secara merata antar anggota group.

| Service | Consumer Group ID | Topics yang Di-consume |
|---|---|---|
| Order Service | dikonfigurasi via env `KAFKA_CONSUMER_GROUP` | `flashsale.inventory.events`, `flashsale.payment.events` |
| Inventory Service | dikonfigurasi via env | `flashsale.order.events` |

**Aturan scaling:** Jumlah instance service idealnya = jumlah partisi. Jika Order Service di-scale menjadi 10 pod dan topic punya 10 partisi, setiap pod mendapat 1 partisi. Jika pod > partisi, kelebihan pod akan idle.

---

## 4. Manual Commit Offset (At-Least-Once Guarantee)

Kedua consumer (`order-service` dan `inventory-service`) menggunakan **`kgo.DisableAutoCommit()`** — offset hanya di-commit setelah pemrosesan benar-benar selesai:

```go
// consumer.go — alur commit yang aman
fetches.EachRecord(func(record *kgo.Record) {
    c.processRecord(ctx, record)  // retry 3x + DLQ fallback
})
// Commit setelah SEMUA record dalam batch selesai
client.CommitUncommittedOffsets(ctx)
```

**Alur safety net jika service crash:**
```
1. Consumer poll 10 records dari Kafka
2. Process record ke-5, crash terjadi
3. Karena offset belum di-commit, Kafka redelivery record 1-10 saat service restart
4. Record 1-4 yang sudah diproses sebelumnya → terdeteksi sebagai duplikat
   via tabel processed_events (Primary Key constraint) → di-skip
5. Record 5-10 diproses ulang dari awal
```

---

## 5. Dead Letter Queue (DLQ)

Event yang gagal setelah **3 kali retry** tidak dibuang, melainkan dikirim ke DLQ topic:

```
Event Gagal (attempt 1) → wait 500ms
Event Gagal (attempt 2) → wait 1000ms  
Event Gagal (attempt 3) → wait 2000ms
                        → sendToDLQ()
```

DLQ record menyertakan metadata di Kafka headers:

| Header Key | Nilai | Fungsi |
|---|---|---|
| `dlq.original.topic` | `flashsale.inventory.events` | Topic asal event |
| `dlq.error` | `"gagal query DB: connection refused"` | Pesan error untuk investigasi |
| `dlq.timestamp` | `2026-06-03T14:00:00Z` | Waktu event gagal |

**Monitoring DLQ via Kafka UI (`http://localhost:18080`):**
- Cek apakah ada message di `flashsale.order.dlq` atau `flashsale.inventory.dlq`
- Jika ada → investigasi root cause via `dlq.error` header
- Untuk replay: copy record dari DLQ ke topic asal menggunakan Kafka producer

---

## 6. Retention Policy

Data Flash Sale bersifat transaksional jangka pendek. State permanen disimpan di PostgreSQL.

| Parameter | Nilai | Alasan |
|---|---|---|
| `log.retention.hours` | 24 jam | Cukup untuk durasi Flash Sale + investigasi bug sehari setelahnya |
| `log.cleanup.policy` | `delete` | Data lama dihapus otomatis setelah retention period |

> Jangan gunakan `compact` policy untuk Flash Sale topics — ini akan menghapus event lama berdasarkan key, bukan waktu.

---

## 7. Konfigurasi Producer (Relay Worker)

Outbox Relay Worker menggunakan konfigurasi producer yang dioptimalkan untuk durabilitas:

| Konfigurasi | Nilai | Alasan |
|---|---|---|
| `RequiredAcks` | `AllISRAcks()` | Kafka hanya dianggap berhasil jika SEMUA in-sync replica mengkonfirmasi |
| `Compression` | `SnappyCompression()` | Kurangi bandwidth ~60-80% tanpa overhead CPU tinggi |
| Retry publish | 5x backoff (200ms → 10s) | Tahan terhadap Kafka leader election sementara |
| Batch timeout | — | `ProduceSync()` — satu record per kali (konsistensi > throughput) |

---

## 8. Topik Auto-Create vs Pre-create

Saat ini `KAFKA_CFG_AUTO_CREATE_TOPICS_ENABLE=true` sehingga topic dibuat otomatis saat pertama kali digunakan. Di production, **pertimbangkan** mematikan fitur ini dan membuat topic secara eksplisit:

```bash
# Buat topic dengan konfigurasi yang tepat
kafka-topics.sh --create \
  --topic flashsale.inventory.events \
  --partitions 10 \
  --replication-factor 3 \
  --config retention.ms=86400000 \
  --bootstrap-server kafka:9092
```

Ini memberikan kontrol penuh atas jumlah partisi, replication factor, dan retention per topic.

---

## 9. Local Development vs Production

Setup saat ini (dalam `docker-compose.yml` dan script lokal) telah dioptimasi sebagai titik seimbang untuk **Local Development / Load Testing di Laptop**:

1. **Kafka (1 Node)**:
   - **Partisi**: Diatur ke **10 partisi** untuk topik utama (`flashsale.inventory.events`, `flashsale.order.events`, dll) agar kode pemrosesan paralel dan konkurensi consumer (Goroutine) tetap teruji maksimal, seolah-olah berjalan di production.
   - **Limit Memori**: JVM Heap dibatasi via `KAFKA_HEAP_OPTS=-Xms512M -Xmx1G` untuk mencegah Kafka memakan habis RAM laptop saat simulasi ribuan Virtual Users (VUs) menggunakan K6.
2. **Redis (1 Node)**:
   - **Limit Memori**: Dibatasi dengan `--maxmemory 512mb` dan policy `allkeys-lru` untuk menghindari kebocoran memori (Out-Of-Memory) di OS lokal.
   - **Persistence**: Untuk performa penulisan maksimal, AOF (Append Only File) disarankan dimatikan di lokal.

**Di Lingkungan Production**, konfigurasi di atas **TIDAK BOLEH** digunakan. Production harus menggunakan:
- **Kafka Cluster** (Minimal 3 Broker) dengan `replication-factor=3` dan alokasi RAM per broker yang jauh lebih besar (4GB - 8GB+).
- **Redis Sentinel/Cluster** (Minimal 1 Master + 2 Replicas) dengan fitur durabilitas (AOF/RDB) yang menyala (ON) untuk mencegah hilangnya data stok dan idempotensi jika terjadi mati listrik (crash).
