# Metrics & Dashboard

> [!WARNING]
> **Prometheus dan Grafana belum diimplementasikan** di proyek ini. Dokumen ini menjelaskan status aktual dan roadmap ke depan.

---

## 1. Status Aktual

| Komponen | Status | Keterangan |
|---|---|---|
| **Distributed Tracing (Jaeger)** | ✅ Diimplementasikan | Semua 6 service mengirim trace via OTLP gRPC |
| **Prometheus** | ❌ Belum ada | Tidak ada di `docker-compose.yml` dan tidak ada dependency di `go.mod` |
| **Grafana** | ❌ Belum ada | Tidak ada container atau konfigurasi dashboard |
| **OTel Collector** | ❌ Belum ada | Service mengirim trace langsung ke Jaeger tanpa Collector |
| **Custom Metrics** | ❌ Belum ada | `flashsale_stock_remaining_total` dll. belum diimplementasikan |

Saat ini, satu-satunya observability backend yang aktif adalah **Jaeger** untuk Distributed Tracing. Lihat [tracing-and-idempotency.md](./tracing-and-idempotency.md) untuk detail lengkap implementasinya.

---

## 2. Roadmap: Rencana Penambahan Metrics

### Arsitektur Target

```
[ Go Services (6 pcs) ]
    │
    │ (Push OTLP: Traces + Metrics + Logs)
    ▼
[ OpenTelemetry Collector ]  ← Perlu ditambahkan di docker-compose
    │
    ├── Traces ──→ [ Jaeger ]              (sudah ada)
    ├── Metrics ──→ [ Prometheus ]         (perlu ditambahkan)
    └── Logs ───→ [ Loki / ELK ]          (opsional)
                        │
                        ▼
                    [ Grafana ]            (perlu ditambahkan)
                  Visualisasi + Alerting
```

### Langkah Implementasi

1. **Tambah OTel Metrics SDK** ke setiap service `go.mod`:
   ```
   go.opentelemetry.io/otel/metric
   go.opentelemetry.io/otel/sdk/metric
   go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc
   ```

2. **Tambah Prometheus + Grafana + OTel Collector** di `docker-compose.yml`:
   ```yaml
   otel-collector:
     image: otel/opentelemetry-collector-contrib:latest
     # ...konfigurasi pipeline traces + metrics

   prometheus:
     image: prom/prometheus:latest
     ports: ["9090:9090"]

   grafana:
     image: grafana/grafana:latest
     ports: ["3000:3000"]
   ```

3. **Inject Custom Metrics** di kode Go:
   ```go
   // Contoh di inventory service
   meter := otel.GetMeterProvider().Meter("inventory-service")
   stockCounter, _ := meter.Int64Gauge("flashsale.stock.remaining",
       metric.WithDescription("Sisa stok produk di Redis"),
   )
   ```

---

## 3. Metrik Kunci yang Direncanakan (RED Metrics)

Setiap microservice sebaiknya mengekspos metrik **RED**:

1. **Rate** — Jumlah request per detik (RPS)
2. **Errors** — Jumlah request yang menghasilkan error (5xx / gRPC Error)
3. **Duration** — Waktu respons (P50, P90, P99 latency)

### Custom Metrics Flash Sale

| Metric Name | Tipe | Keterangan | Sumber Data |
|---|---|---|---|
| `flashsale.stock.remaining` | Gauge | Sisa stok di Redis per `product_id` | Redis `HGET stock:{product_id}` |
| `flashsale.checkout.success.total` | Counter | Total checkout berhasil mendapat stok | Inventory Service Lua Script return 1 |
| `flashsale.checkout.failed.total` | Counter | Total checkout ditolak (stok habis) | Inventory Service Lua Script return 0 |
| `flashsale.kafka.consumer.lag` | Gauge | Jumlah event yang belum diproses | franz-go consumer group offset |
| `flashsale.outbox.pending.total` | Gauge | Jumlah outbox status PENDING (per Service) | PostgreSQL `COUNT(*)` dari `db_inventory`, `db_order`, `db_payment` |
| `flashsale.outbox.failed.total` | Counter | Jumlah outbox status FAILED (per Service) | PostgreSQL `COUNT(*)` dari `db_inventory`, `db_order`, `db_payment` |
| `flashsale.circuit_breaker.state` | Gauge | State CB per service (0=closed, 1=open) | gobreaker state callback |
| `flashsale.worker.timeout.cancelled` | Counter | Total order yang dibatalkan oleh Timeout Worker | Worker di Order Service |
| `flashsale.worker.reconciliation.fixed` | Counter | Total stok "bocor" yang dikembalikan | Reconciliation Job di Inventory Service |

### Dashboard Grafana yang Direncanakan

| Panel | Metrik | Tampilan |
|---|---|---|
| **Request Rate (API Gateway)** | `rate(http_requests_total[1m])` | Line chart |
| **Error Rate** | `rate(http_requests_total{status=~"5.."}[1m])` | Line chart |
| **Checkout Latency P99** | `histogram_quantile(0.99, ...)` | Gauge |
| **Sisa Stok Real-time** | `flashsale.stock.remaining` | Stat panel |
| **Saga Completion Rate** | checkout vs paid vs cancelled | Bar chart |
| **Kafka Consumer Lag** | `flashsale.kafka.consumer.lag` | Line chart (alert jika > 1000) |
| **Outbox FAILED** | `flashsale.outbox.failed.total` | Alert panel |
| **Circuit Breaker State** | `flashsale.circuit_breaker.state` | State timeline |

---

## 4. Alternatif Monitoring Saat Ini

Sambil menunggu Prometheus/Grafana diimplementasikan, gunakan alat berikut:

### Jaeger UI (`http://localhost:16686`)
- Lacak latency per request end-to-end
- Identifikasi span mana yang paling lambat
- Debug error dengan melihat span yang berwarna merah

### PostgreSQL Query Monitoring Outbox (Manual)
Karena tabel `outbox_messages` kini terpisah di **3 database berbeda** (`db_inventory`, `db_order`, `db_payment`), Anda harus menjalankan query ini di masing-masing database untuk memonitor Relay Worker-nya:

```sql
-- Health check Relay Worker (Jalankan di tiap DB)
SELECT status, COUNT(*), MAX(created_at) as latest
FROM outbox_messages
GROUP BY status;

-- Alert jika ada FAILED (Jalankan di tiap DB)
SELECT * FROM outbox_messages
WHERE status = 'FAILED'
ORDER BY created_at DESC LIMIT 10;
```

### Redis CLI Real-time Stock
```bash
# Cek sisa stok real-time
redis-cli GET "stock:prod_1"
redis-cli GET "stock:prod_2"

# Monitor semua key Redis secara real-time
redis-cli MONITOR
```

### Docker Stats
```bash
# CPU dan Memory usage semua container
docker stats

# Log service tertentu
docker logs flashsale-inventory -f --tail=100
```
