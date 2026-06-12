ifneq (,$(wildcard ./.env))
    include .env
    export
endif

.PHONY: up down prod-up prod-down

# Menyalakan keseluruhan sistem (Infra + Microservices Kontainer) secara bersih
up:
	@if [ ! -f .env ]; then cp .env.example .env; echo "✅ Created .env from .env.example"; fi
	@echo "Menyalakan keseluruhan sistem (Infrastruktur + Aplikasi Go Kontainer)..."
	docker-compose --profile app up -d --build
	@echo "Menunggu Redis siap..."
	@sleep 5
	@echo "Memastikan Kafka topics tersedia..."
	@docker-compose exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@echo "Menginisialisasi stok awal di Redis untuk keperluan demonstrasi..."
	@docker-compose exec -T redis redis-cli SET "stock:product-flashsale-001" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_1" 200 || true
	@docker-compose exec -T redis redis-cli SET "stock:prod_2" 200 || true
	@echo "✅ Sistem siap digunakan! Stok telah di-set."

# Mematikan keseluruhan sistem secara bersih dan menghapus volume
down:
	@echo "Mematikan keseluruhan sistem secara bersih..."
	docker-compose --profile app down -v

# Menjalankan infrastruktur production-ready (Redis Sentinel, dll)
prod-up:
	@if [ ! -f .env ]; then cp .env.example .env; echo "✅ Created .env from .env.example"; fi
	@echo "Menyalakan seluruh layanan dan infrastruktur PRODUCTION..."
	docker-compose -f docker-compose.prod.yml --profile app up --build -d
	@echo "Memastikan Kafka topics tersedia (PROD)..."
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true
	@docker-compose -f docker-compose.prod.yml exec -T kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists >/dev/null 2>&1 || true

# Mematikan infrastruktur production
prod-down:
	@echo "Mematikan seluruh layanan dan infrastruktur PRODUCTION..."
	docker-compose -f docker-compose.prod.yml --profile app down -v
