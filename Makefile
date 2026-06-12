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
