#!/usr/bin/env bash
# ==============================================================================
# stop-local.sh
# Mematikan semua Go microservices yang berjalan di HOST.
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
PID_DIR="$ROOT_DIR/.local-pids"

SERVICES=("api-gateway" "auth-service" "product-service" "inventory-service" "order-service" "payment-service")

echo ""
echo "══════════════════════════════════════════════════════"
echo "  🛑 Mematikan SEMUA Go Services di HOST (Local)"
echo "══════════════════════════════════════════════════════"
echo ""

if [ ! -d "$PID_DIR" ]; then
  echo "⚠️  Tidak ada service yang berjalan (direktori PID tidak ditemukan)."
  exit 0
fi

for name in "${SERVICES[@]}"; do
  pid_file="$PID_DIR/${name}.pid"
  if [ -f "$pid_file" ]; then
    pid=$(cat "$pid_file")
    if kill -0 "$pid" 2>/dev/null; then
      echo "🛑 Mematikan $name (PID $pid)..."
      # Kirim SIGTERM dulu (graceful shutdown)
      kill -TERM "$pid" 2>/dev/null || true
      sleep 1
      # Jika masih jalan, paksa kill
      if kill -0 "$pid" 2>/dev/null; then
        kill -KILL "$pid" 2>/dev/null || true
        echo "   ⚠️  $name di-force kill (SIGKILL)"
      else
        echo "   ✅ $name berhenti dengan bersih"
      fi
    else
      echo "⚠️  $name (PID $pid) sudah tidak berjalan"
    fi
    rm -f "$pid_file"
  else
    echo "⚠️  $name tidak ditemukan di PID registry"
  fi
done

echo ""
echo "🧹 Membersihkan sisa orphaned processes jika ada..."
pkill -f "/api-gateway" 2>/dev/null || true
pkill -f "/auth-service" 2>/dev/null || true
pkill -f "/product-service" 2>/dev/null || true
pkill -f "/inventory-service" 2>/dev/null || true
pkill -f "/order-service" 2>/dev/null || true
pkill -f "/payment-service" 2>/dev/null || true

echo "  ✅ Semua local services telah dimatikan."
echo "══════════════════════════════════════════════════════"
echo ""
