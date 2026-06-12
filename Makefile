ifneq (,$(wildcard ./.env))
    include .env
    export
endif

.PHONY: up down

# Menjalankan infrastruktur production-ready (Redis Sentinel, dll)
up:
	@if [ ! -f .env ]; then cp .env.example .env; echo "✅ Created .env from .env.example"; fi
	@echo "Menyalakan seluruh layanan dan infrastruktur PRODUCTION..."
	docker-compose -f docker-compose.prod.yml --profile app up --build -d
	@echo "Memastikan Kafka topics tersedia (PROD)..."
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true

# Mematikan infrastruktur production
down:
	@echo "Mematikan seluruh layanan dan infrastruktur PRODUCTION..."
	docker-compose -f docker-compose.prod.yml --profile app down -v

.PHONY: seed-stock-soak-prod test-no-oversell seed-stock

# Seed khusus untuk Soak Test (Prod Infra dengan Sentinel)
seed-stock-soak-prod:
	@echo "Menginisialisasi stok SOAK TEST di Redis Master (Prod) (1.000.000)..."
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:product-flashsale-001" 1000000 || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli SET "stock:prod_1" 1000000 || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli DEL "purchased:product-flashsale-001" || true
	@docker-compose -f docker-compose.prod.yml exec -T redis-master redis-cli DEL "orders:product-flashsale-001" || true
	@echo "✅ Stok berhasil di-reset menjadi 1.000.000 di Redis Master!"

# Mengisi ulang/mereset stok awal di Redis
seed-stock:
	@echo "Menginisialisasi stok awal di Redis..."
	@docker-compose exec -T redis redis-cli SET "stock:product-flashsale-001" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_1" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_2" 200 || true
	@echo "✅ Stok berhasil di-reset menjadi 200!"

K6_PROMETHEUS_ENV = K6_PROMETHEUS_RW_SERVER_URL=http://localhost:9090/api/v1/write K6_PROMETHEUS_RW_TREND_STATS="p(90),p(95),p(99),max,min,avg"
K6_PROMETHEUS_OUT = --out experimental-prometheus-rw

# Menjalankan No-Oversell Test
test-no-oversell:
	@echo "Menjalankan K6 No-Oversell Test (Verifikasi keamanan stok)..."
	@make seed-stock
	$(K6_PROMETHEUS_ENV) k6 run -e TARGET=backend $(K6_PROMETHEUS_OUT) performance-tests/k6/04_no_oversell.js
