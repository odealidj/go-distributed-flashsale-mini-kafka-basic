# Panduan Implementasi OpenTelemetry di Golang

Dokumen ini menjelaskan bagaimana kode Golang dalam arsitektur Flash Sale ini terhubung dengan **Jaeger (Tracing)** dan **Prometheus/Grafana (Metrics)** menggunakan standar vendor-neutral **OpenTelemetry (OTel)**.

---

## 1. Konsep Dasar

Dalam sistem modern, kita **tidak** menginstal *library* spesifik vendor (seperti `go-jaeger` atau `go-prometheus`) ke dalam kode bisnis. 

Sebaliknya, kode Go kita hanya menggunakan **OpenTelemetry SDK**. Go kemudian "mendorong" (push) data Telemetry (Traces & Metrics) melalui protokol standar **OTLP (OpenTelemetry Protocol) via gRPC**. Backend apa pun (Jaeger, Prometheus, Datadog) yang bisa menerima OTLP dapat memproses data tersebut tanpa perlu mengubah satu baris pun kode Golang kita.

---

## 2. Struktur Direktori

Seluruh logika inisialisasi Telemetry dipusatkan di dalam satu modul *shared* agar semua *microservice* memiliki standar *monitoring* yang seragam:

```
shared/pkg/telemetry/
├── extractor.go      # Mengekstrak TraceID dari Kafka Record Headers
├── metrics.go        # Custom Middleware untuk menghitung RED Metrics
└── telemetry.go      # Inisialisasi OTLP Exporter, Tracer, dan Meter
```

---

## 3. Bagaimana Go Terhubung ke Jaeger (Tracing)

### Langkah 1: Inisialisasi Exporter & Provider
File: [`shared/pkg/telemetry/telemetry.go`](../../shared/pkg/telemetry/telemetry.go)

Pada fungsi `InitTelemetry(...)`, Go membuka koneksi gRPC ke port penerima OTLP (default: `:4317` yang di-listen oleh Jaeger).

```go
// 1. Membuat OTLP Exporter via gRPC
traceExp, err := otlptracegrpc.New(ctx,
    otlptracegrpc.WithInsecure(),
    otlptracegrpc.WithEndpoint("localhost:4317"), // Alamat OTLP receiver (Jaeger)
)

// 2. Mendaftarkan Exporter ke dalam Tracer Provider
res := resource.NewWithAttributes(
    semconv.SchemaURL,
    semconv.ServiceNameKey.String("order-service"), // Mengidentifikasi service di UI Jaeger
)

tp := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(traceExp),
    sdktrace.WithResource(res),
)
otel.SetTracerProvider(tp)
```

### Langkah 2: Middleware Penangkapan Otomatis
Framework **Go-Kratos** sudah memiliki integrasi *native* dengan OpenTelemetry. Di file `main.go` setiap service, kita cukup memasang *middleware*:

```go
// File: api-gateway/cmd/api-gateway/main.go
httpSrv := kratoshttp.NewServer(
    kratoshttp.Address(":18000"),
    // tracing.Server() adalah middleware Kratos untuk menangkap HTTP Request menjadi Span
    kratoshttp.Middleware(tracing.Server(), telemetry.ServerMetrics()),
)
```

**Bagaimana ini bekerja?**
1. Saat user melakukan `POST /checkout`, `tracing.Server()` mencegat HTTP request tersebut.
2. Ia membuat sebuah *TraceID* baru.
3. Saat API Gateway memanggil Inventory Service via gRPC, Kratos secara otomatis "menyuntikkan" (*inject*) *TraceID* tersebut ke dalam *gRPC Metadata*.
4. Inventory Service membaca *TraceID* itu dan melanjutkannya sebagai *Child Span*, sehingga keseluruhan alur terhubung sebagai satu pohon rute visual di Jaeger.

---

## 4. Bagaimana Go Terhubung ke Prometheus & Grafana (Metrics)

Berbeda dengan cara tradisional di mana Prometheus harus "menarik" (*pull*) metrik dari endpoint `/metrics`, pada sistem ini Go "mendorong" (*push*) metriknya melalui jalur OTLP gRPC yang sama dengan Tracing!

### Langkah 1: Menangkap Metrik Bawaan Golang (Runtime)
File: [`shared/pkg/telemetry/telemetry.go`](../../shared/pkg/telemetry/telemetry.go)

```go
// 1. Membuat OTLP Metric Exporter
metricExp, _ := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithInsecure(), otlpmetricgrpc.WithEndpoint("localhost:4317"))

// 2. Mendaftarkannya ke Meter Provider
mp := sdkmetric.NewMeterProvider(
    sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(15*time.Second))),
    sdkmetric.WithResource(res),
)
otel.SetMeterProvider(mp)

// 3. Menyalakan Runtime Monitoring bawaan Go
// Ini akan OTOMATIS membaca jumlah Goroutine aktif, penggunaan memori (Heap), CPU, dan durasi Garbage Collection (GC) Go!
runtime.Start(runtime.WithMeterProvider(mp))
```

### Langkah 2: Menangkap RED Metrics (Request, Error, Duration)
File: [`shared/pkg/telemetry/metrics.go`](../../shared/pkg/telemetry/metrics.go)

Kita membuat custom *middleware* Kratos bernama `ServerMetrics()`. Fungsinya adalah mencegat setiap request HTTP/gRPC, menghitung berapa lama waktu eksekusinya, dan mendata apakah hasilnya sukses atau *error*.

```go
func ServerMetrics() middleware.Middleware {
    meter := otel.Meter("kratos-server")

    // Mendefinisikan Alat Ukur
    reqCounter, _ := meter.Int64Counter("kratos_requests_total")
    errCounter, _ := meter.Int64Counter("kratos_errors_total")
    latencyHistogram, _ := meter.Float64Histogram("kratos_request_duration_seconds")

    return func(handler middleware.Handler) middleware.Handler {
        return func(ctx context.Context, req interface{}) (interface{}, error) {
            startTime := time.Now()
            
            // 1. Catat bahwa 1 request masuk
            reqCounter.Add(ctx, 1)

            // 2. Eksekusi kode bisnis sesungguhnya
            reply, err := handler(ctx, req)

            // 3. Catat jika balasan ini adalah sebuah Error
            if err != nil {
                errCounter.Add(ctx, 1)
            }

            // 4. Catat waktu eksekusi / Latency (durasi request berlangsung)
            latencyHistogram.Record(ctx, time.Since(startTime).Seconds())

            return reply, err
        }
    }
}
```

Alat-alat ukur inilah (`kratos_requests_total`, `kratos_errors_total`, dll) yang pada akhirnya ditranslasikan oleh receiver dan muncul di dalam _Dashboard_ Grafana yang Anda lihat saat Load Testing K6.

---

## 5. Merangkai Semuanya di File Main (`main.go`)

Setiap microservice di-*bootstrapping* dengan baris kode yang sangat sederhana ini:

```go
func main() {
    // ...
    // 1. Inisialisasi koneksi OpenTelemetry di awal aplikasi
    cleanupTelemetry, err := telemetry.InitTelemetry(context.Background(), "order-service", "localhost:4317")
    if err != nil {
        panic(err)
    }
    // Pastikan koneksi ditutup rapi saat aplikasi mati
    defer cleanupTelemetry(context.Background())

    // ...
    // 2. Sisipkan Middleware saat mendefinisikan Server
    grpcSrv := grpc.NewServer(
        grpc.Middleware(
            tracing.Server(),       // Middleware untuk Traces (Jaeger)
            telemetry.ServerMetrics(), // Middleware untuk Metrics (Prometheus/Grafana)
        ),
    )
    // ...
}
```

Dengan desain modular seperti ini, kita memastikan kode bisnis yang ada di dalam *Usecase* tidak terkotori oleh *logic* telemetri, namun seluruh sistem tetap terpantau dengan sangat komprehensif.
