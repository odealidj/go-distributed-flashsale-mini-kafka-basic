#!/usr/bin/env bash
# ==============================================================================
# run-local.sh
# Menjalankan semua Go microservices langsung di HOST (bukan container),
# sehingga bisa di-debug dengan VS Code / GoLand / Delve.
#
# Infrastruktur (Postgres, Redis, Kafka) tetap berjalan via Docker/Podman.
# ==============================================================================

set -e

# Load variabel dari .env di root proyek
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
ENV_FILE="$ROOT_DIR/.env"

if [ ! -f "$ENV_FILE" ]; then
  echo "❌ File .env tidak ditemukan di $ROOT_DIR"
  exit 1
fi
set -o allexport
source "$ENV_FILE"
set +o allexport

# ==============================================================================
# Override endpoint untuk mode lokal:
# Services berjalan di localhost, bukan di nama container Docker
# ==============================================================================
export DB_HOST=localhost
export REDIS_HOST=localhost
export KAFKA_HOST=localhost
export JAEGER_HOST=localhost

# Port tetap sesuai .env (port yang di-expose ke host)
# DB_PORT=15432, REDIS_PORT=16379, KAFKA_PORT=19092, JAEGER_OTLP_GRPC_PORT=14317

# Override path JWT key agar menunjuk ke file di host (bukan path container /app/certs/...)
export JWT_PUBLIC_KEY_PATH="$ROOT_DIR/certs/public.pem"
export JWT_PRIVATE_KEY_PATH="$ROOT_DIR/certs/private.pem"

# Override Redis addr agar services bisa terhubung ke port host (bukan default :6379)
export REDIS_ADDR="localhost:${REDIS_PORT:-16379}"

# Endpoint antar-service: gunakan localhost + port host
export PRODUCT_SERVICE_ENDPOINT="localhost:${PRODUCT_SERVICE_PORT:-19001}"
export INVENTORY_SERVICE_ENDPOINT="localhost:${INVENTORY_SERVICE_PORT:-19002}"
export PAYMENT_SERVICE_ENDPOINT="localhost:${PAYMENT_SERVICE_PORT:-19003}"
export AUTH_SERVICE_ENDPOINT="localhost:${AUTH_SERVICE_PORT:-19004}"
export ORDER_SERVICE_ENDPOINT="localhost:${ORDER_SERVICE_PORT:-19005}"

# Override port service agar berjalan di port host (bukan internal container)
# Override port service agar berjalan di port host (bukan internal container)
export API_GATEWAY_PORT="${API_GATEWAY_PORT:-18000}"
export PRODUCT_SERVICE_PORT="${PRODUCT_SERVICE_PORT:-19001}"
export INVENTORY_SERVICE_PORT="${INVENTORY_SERVICE_PORT:-19002}"
export PAYMENT_SERVICE_PORT="${PAYMENT_SERVICE_PORT:-19003}"
export AUTH_SERVICE_PORT="${AUTH_SERVICE_PORT:-19004}"
export ORDER_SERVICE_PORT="${ORDER_SERVICE_PORT:-19005}"

# Direktori untuk menyimpan PID file setiap service
PID_DIR="$ROOT_DIR/.local-pids"
LOG_DIR="$ROOT_DIR/.local-logs"
mkdir -p "$PID_DIR" "$LOG_DIR"

# ==============================================================================
# Fungsi helper
# Penggunaan: start_service <name> <pkg_dir> [KEY=VALUE ...]
#   pkg_dir  : path direktori package (bukan file main.go), relatif dari ROOT_DIR
#              contoh: "auth-service/cmd/auth-service"
#   KEY=VALUE: env var tambahan yang hanya berlaku untuk proses ini (opsional)
#              contoh: "DATABASE_URL=postgres://..."
# ==============================================================================
start_service() {
  local name="$1"
  local pkg_dir="$2"
  shift 2
  # Sisa argumen ($@) adalah env var tambahan: KEY=VALUE ...

  local pid_file="$PID_DIR/${name}.pid"
  local log_file="$LOG_DIR/${name}.log"

  if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
    echo "⚠️  $name sudah berjalan (PID $(cat "$pid_file")). Lewati."
    return
  fi

  echo "🚀 Menjalankan $name..."
  cd "$ROOT_DIR"
  # Gunakan `go run ./dir/` (direktori) agar semua file .go dalam package
  # ikut dikompile — termasuk wire_gen.go yang dihasilkan wire.
  # `env "$@"` meneruskan env var tambahan hanya untuk proses ini.
  nohup env "$@" go run "./${pkg_dir}/" >> "$log_file" 2>&1 &
  local pid=$!
  echo "$pid" > "$pid_file"
  
  # Cek apakah proses mati mendadak (misal karena port tabrakan)
  sleep 1
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "   ❌ ERROR: $name gagal berjalan!"
    echo "   📜 Log terakhir ($log_file):"
    tail -n 5 "$log_file" | sed 's/^/      /g'
    echo ""
  else
    echo "   ✅ $name berjalan (PID $pid) → log: $log_file"
  fi
}

# Konstruksi DATABASE_URL per service untuk mode lokal.
# Menggunakan port host (DB_PORT=15432) bukan port internal container (5432).
_DB_BASE="postgres://${POSTGRES_USER:-root}:${POSTGRES_PASSWORD:-rootpassword}@localhost:${DB_PORT:-15432}"

# ==============================================================================
# Inisialisasi Kafka Topics (Mencegah UNKNOWN_TOPIC_OR_PARTITION)
# ==============================================================================
echo "⚙️  Memastikan Kafka topics tersedia..."
if command -v podman &> /dev/null; then
  podman exec flashsale-kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists &>/dev/null || true
  podman exec flashsale-kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists &>/dev/null || true
  podman exec flashsale-kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists &>/dev/null || true
elif command -v docker &> /dev/null; then
  docker exec flashsale-kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.inventory.events --partitions 10 --replication-factor 1 --if-not-exists &>/dev/null || true
  docker exec flashsale-kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.order.events --partitions 10 --replication-factor 1 --if-not-exists &>/dev/null || true
  docker exec flashsale-kafka /opt/bitnami/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic flashsale.payment.events --partitions 10 --replication-factor 1 --if-not-exists &>/dev/null || true
fi

# ==============================================================================
# Jalankan semua services
# ==============================================================================
echo ""
echo "══════════════════════════════════════════════════════"
echo "  🖥️  Menjalankan SEMUA Go Services di HOST (Local)"
echo "══════════════════════════════════════════════════════"
echo ""

# auth-service: DATABASE_URL wajib (tidak ada fallback ke individual vars)
start_service "auth-service" "auth-service/cmd/auth-service" \
  "DATABASE_URL=${_DB_BASE}/${DB_AUTH:-db_auth}?sslmode=disable"

# product-service: punya wire_gen.go → wajib pakai direktori
start_service "product-service" "product-service/cmd/product-service" \
  "DATABASE_URL=${_DB_BASE}/${DB_PRODUCT:-db_product}?sslmode=disable"

# inventory-service: punya wire_gen.go → wajib pakai direktori
start_service "inventory-service" "inventory-service/cmd/inventory-service" \
  "DATABASE_URL=${_DB_BASE}/${DB_INVENTORY:-db_inventory}?sslmode=disable"

# order-service: punya fallback individual vars, tapi eksplisit lebih aman
start_service "order-service" "order-service/cmd/order-service" \
  "DATABASE_URL=${_DB_BASE}/${DB_ORDER:-db_order}?sslmode=disable"

# payment-service: punya wire_gen.go → wajib pakai direktori
start_service "payment-service" "payment-service/cmd/payment-service" \
  "DATABASE_URL=${_DB_BASE}/${DB_PAYMENT:-db_payment}?sslmode=disable"

# API Gateway dijalankan terakhir karena bergantung pada service lain
sleep 2
start_service "api-gateway" "api-gateway/cmd/api-gateway"

echo ""
echo "══════════════════════════════════════════════════════"
echo "  ✅ Semua service sedang berjalan di host!"
echo ""
echo "  📌 Tips Debugging:"
echo "     - VS Code  : F5 pada main.go service yang ingin di-debug"
echo "     - GoLand   : Klik ikon Debug ▶ di sebelah func main()"
echo "     - Manual   : dlv debug ./<service>/cmd/<service>/main.go"
echo ""
echo "  📋 Cek log: tail -f .local-logs/<nama-service>.log"
echo "  🛑 Matikan: make stop-local-all"
echo "══════════════════════════════════════════════════════"
echo ""
