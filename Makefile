ifneq (,$(wildcard ./.env))
    include .env
    export
endif

.PHONY: infra-up infra-down infra-logs proto
.PHONY: run-api-gateway run-product run-product-service run-inventory run-inventory-service run-order run-order-service run-payment run-payment-service
.PHONY: stop-api-gateway stop-product stop-product-service stop-inventory stop-inventory-service stop-order stop-order-service stop-payment stop-payment-service
.PHONY: run-all stop-all run-local-all stop-local-all up down seed-stock create-topics

# ==============================================================================
# INFRASTRUCTURE (Docker Compose)
# ==============================================================================

# Menjalankan HANYA infrastruktur pendukung (Postgres, Redis, Kafka, Jaeger, Nginx)
# Catatan: Kontainer aplikasi Go diabaikan karena berada di bawah profil 'app'
infra-up:
	@echo "Menyalakan HANYA kontainer infrastruktur pendukung..."
	docker-compose up -d

# Mematikan HANYA infrastruktur pendukung
infra-down:
	@echo "Mematikan kontainer infrastruktur pendukung..."
	docker-compose down -v

# Melihat log infrastruktur
infra-logs:
	docker-compose logs -f


# Menjalankan infrastruktur production-ready (Redis Sentinel, dll)
prod-up:
	@if [ ! -f .env ]; then cp .env.example .env; echo "✅ Created .env from .env.example"; fi
	@echo "Menyalakan seluruh layanan dan infrastruktur PRODUCTION..."
	docker-compose -f docker-compose.prod.yml --profile app up --build -d
	@$(MAKE) create-topics-prod

# Mematikan infrastruktur production
prod-down:
	@echo "Mematikan seluruh layanan dan infrastruktur PRODUCTION..."
	docker-compose -f docker-compose.prod.yml --profile app down -v

# ==============================================================================
# MICROSERVICES (Docker Container Orchestration)
# ==============================================================================

run-api-gateway:
	@echo "Membangun & menjalankan kontainer API Gateway..."
	docker-compose --profile app up -d --build --no-deps api-gateway

stop-api-gateway:
	@echo "Mematikan & menghapus kontainer API Gateway..."
	docker-compose --profile app stop api-gateway

run-product run-product-service:
	@echo "Membangun & menjalankan kontainer Product Service..."
	docker-compose --profile app up -d --build --no-deps product-service

stop-product stop-product-service:
	@echo "Mematikan & menghapus kontainer Product Service..."
	docker-compose --profile app stop product-service

run-inventory run-inventory-service:
	@echo "Membangun & menjalankan kontainer Inventory Service..."
	docker-compose --profile app up -d --build --no-deps inventory-service

stop-inventory stop-inventory-service:
	@echo "Mematikan & menghapus kontainer Inventory Service..."
	docker-compose --profile app stop inventory-service

run-order run-order-service:
	@echo "Membangun & menjalankan kontainer Order Service..."
	docker-compose --profile app up -d --build --no-deps order-service

stop-order stop-order-service:
	@echo "Mematikan & menghapus kontainer Order Service..."
	docker-compose --profile app stop order-service

run-payment run-payment-service:
	@echo "Membangun & menjalankan kontainer Payment Service..."
	docker-compose --profile app up -d --build --no-deps payment-service

stop-payment stop-payment-service:
	@echo "Mematikan & menghapus kontainer Payment Service..."
	docker-compose --profile app stop payment-service

run-auth run-auth-service:
	@echo "Membangun & menjalankan kontainer Auth Service..."
	docker-compose --profile app up -d --build --no-deps auth-service

stop-auth stop-auth-service:
	@echo "Mematikan & menghapus kontainer Auth Service..."
	docker-compose --profile app stop auth-service


# ==============================================================================
# BATCH COMMANDS
# ==============================================================================

# Menjalankan seluruh microservices Go sebagai kontainer tanpa merekreasi dependensi infra
run-all:
	@echo "Membangun & menjalankan SELURUH kontainer microservices Go..."
	docker-compose --profile app up -d --build --no-deps api-gateway auth-service product-service inventory-service order-service payment-service
	@make create-topics

stop-all:
	@echo "Mematikan & menghapus SELURUH kontainer microservices Go..."
	docker-compose --profile app stop api-gateway auth-service product-service inventory-service order-service payment-service

# ==============================================================================
# LOCAL (HOST) COMMANDS — Cocok untuk Debugging
# ==============================================================================

# Menjalankan semua Go services LANGSUNG di host (bukan container)
# Infrastruktur (Postgres, Redis, Kafka) tetap pakai Docker via make infra-up
# Gunakan ini untuk debugging dengan VS Code / GoLand / Delve
run-local-all:
	@echo "Menjalankan semua Go services di HOST untuk debugging..."
	@bash scripts/run-local.sh

# Mematikan semua Go services yang berjalan di host
stop-local-all:
	@echo "Mematikan semua Go services lokal..."
	@bash scripts/stop-local.sh

# Menyalakan keseluruhan sistem (Infra + Microservices Kontainer) secara bersih
up:
	@if [ ! -f .env ]; then cp .env.example .env; echo "✅ Created .env from .env.example"; fi
	@echo "Menyalakan keseluruhan sistem (Infrastruktur + Aplikasi Go Kontainer)..."
	docker-compose --profile app up -d --build
	@echo "Menunggu Redis siap..."
	@sleep 5
	@make create-topics
	@echo "Menginisialisasi stok awal di Redis untuk keperluan demonstrasi..."
	@docker-compose exec -T redis redis-cli SET "stock:product-flashsale-001" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_1" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_2" 200 || true
	@echo "✅ Sistem siap digunakan! Stok telah di-set."

create-topics:
	@echo "Memastikan Kafka topics tersedia..."
	@docker-compose exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true

create-topics-prod:
	@echo "Memastikan Kafka topics tersedia (PROD)..."
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true

# Mengisi ulang/mereset stok awal di Redis
seed-stock:
	@echo "Menginisialisasi stok awal di Redis..."
	@docker-compose exec -T redis redis-cli SET "stock:product-flashsale-001" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_1" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_2" 200 || true
	@echo "✅ Stok berhasil di-reset menjadi 200!"

# Mengisi ulang/mereset stok awal di Redis (Prod Infra dengan Sentinel)
seed-stock-prod:
	@echo "Menginisialisasi stok awal di Redis Master (Prod)..."
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:product-flashsale-001" 200 || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:prod_1" 200 || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:prod_2" 200 || true
	@echo "✅ Stok berhasil di-reset menjadi 200 di Redis Master!"

# Mematikan keseluruhan sistem secara bersih dan menghapus volume
down:
	@echo "Mematikan keseluruhan sistem secara bersih..."
	docker-compose --profile app down -v


# ==============================================================================
# UTILS
# ==============================================================================

# Men-generate kode Go dari file .proto
proto:
	cd proto && protoc --go_out=paths=source_relative:. \
	       --go-grpc_out=paths=source_relative:. \
	       auth/v1/auth.proto \
	       inventory/v1/inventory.proto \
	       order/v1/order.proto \
	       payment/v1/payment.proto \
	       product/v1/product.proto

# Menjalankan Integration Tests menggunakan Testcontainers-Go
test-integration:
	@echo "Menjalankan Integration Tests..."
	cd inventory-service && go test -v -tags=integration ./...

# Menjalankan Unit/Integration Tests untuk API Gateway
test-api-gateway:
	@echo "Menjalankan API Gateway Tests..."
	cd api-gateway && go test -v ./...


# ==============================================================================
# PERFORMANCE TESTS (k6)
# ==============================================================================

K6_PROMETHEUS_ENV = K6_PROMETHEUS_RW_SERVER_URL=http://localhost:9090/api/v1/write K6_PROMETHEUS_RW_TREND_STATS="p(90),p(95),p(99),max,min,avg"
K6_PROMETHEUS_OUT = --out experimental-prometheus-rw

# Menjalankan Load Test Frontend (Target: Nginx)
# Mengekspektasikan HTTP 429 sebagai kewajaran
test-load-nginx:
	@echo "Menjalankan K6 Load Test (Target: Nginx Rate Limiter)..."
	K6_TARGET=nginx $(K6_PROMETHEUS_ENV) k6 run -e TARGET=nginx $(K6_PROMETHEUS_OUT) performance-tests/k6/03_checkout_pubsub.js

# Menjalankan Load Test Backend (Target: API Gateway)
# Mengekspektasikan 0% HTTP 429 dan melihat batas CPU/RAM/Goroutine
test-load-backend:
	@echo "Menjalankan K6 Load Test (Target: Backend Microservices)..."
	@echo "--- 1. Raw Checkout (Polling) ---"
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/01_raw_checkout_polling.js
	@echo "--- 2. Checkout Long Polling ---"
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/02_checkout_long_polling.js
	@echo "--- 3. Checkout PubSub ---"
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/03_checkout_pubsub.js
	@echo "--- 4. Checkout SSE ---"
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/04_checkout_sse.js

# Menjalankan Idempotency Test
test-idempotency:
	@echo "Menjalankan K6 Idempotency Test (Mencegah Double Checkout)..."
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/02_idempotency_test.js

# Seed khusus untuk Soak Test (1 Juta Stok agar Kafka terus bekerja)
seed-stock-soak:
	@echo "Menginisialisasi stok SOAK TEST di Redis (1.000.000)..."
	@docker-compose exec -T redis redis-cli SET "stock:prod_1" 1000000
	@docker-compose exec -T redis redis-cli DEL "purchased:prod_1"
	@docker-compose exec -T redis redis-cli DEL "orders:prod_1"
	@echo "✅ Stok berhasil di-reset menjadi 1.000.000!"

# Seed khusus untuk Soak Test (Prod Infra dengan Sentinel)
seed-stock-soak-prod:
	@echo "Menginisialisasi stok SOAK TEST di Redis Master (Prod) (1.000.000)..."
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:product-flashsale-001" 1000000 || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:prod_1" 1000000 || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli DEL "purchased:product-flashsale-001" || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli DEL "orders:product-flashsale-001" || true
	@echo "✅ Stok berhasil di-reset menjadi 1.000.000 di Redis Master!"

# Menjalankan Soak Test (3 Menit)
test-soak-3m:
	@echo "Menjalankan K6 Soak Test (Pemanasan 3 menit) & Mengirim Metrics ke Grafana..."
	@make seed-stock-soak
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/03_soak_test_3m.js

# Menjalankan Soak Test (30 Menit)
test-soak:
	@echo "Menjalankan K6 Soak Test (Ketahanan 30 menit) & Mengirim Metrics ke Grafana..."
	@make seed-stock-soak
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/03_soak_test.js

# Menjalankan No-Oversell Test
test-no-oversell:
	@echo "Menjalankan K6 No-Oversell Test (Verifikasi keamanan stok)..."
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/04_no_oversell.js

# Menjalankan Compensation Test
test-compensation:
	@echo "Menjalankan K6 Compensation Test (Persiapan Saga Kompensasi)..."
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/05_compensation_test.js

# Menjalankan Spike Test Prod
test-spike-prod:
	@echo "Menjalankan K6 Spike Test (Simulasi lonjakan Flash Sale di infrastruktur prod)..."
	@make seed-stock-prod
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/06_flashsale_spike_prod.js

# Menjalankan Breakpoint Test (Mencari Limit Sistem)
test-breakpoint:
	@echo "Menjalankan K6 Breakpoint Test (Menaikkan beban hingga 3000 RPS untuk mencari limit)..."
	@make seed-stock-soak-prod
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/07_find_breakpoint.js

