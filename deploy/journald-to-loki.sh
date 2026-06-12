#!/usr/bin/env bash
# ============================================================
# journald-to-loki.sh
#
# Skrip ini berjalan di HOST (bukan di dalam container).
# Fungsinya: membaca log seluruh container Podman dari journald
# lalu menulisnya ke file .log per service, agar Promtail
# dapat mengambil dan mengirimkannya ke Loki.
#
# Cara pakai: bash deploy/journald-to-loki.sh
# Atau jalankan sebagai background service.
# ============================================================

set -euo pipefail

LOG_DIR="/tmp/flashsale-logs"
mkdir -p "$LOG_DIR"

# Daftar service yang akan dikumpulkan log-nya
SERVICES=(
  "api-gateway"
  "auth-service"
  "product-service"
  "inventory-service"
  "order-service"
  "payment-service"
  "nginx"
  "kafka"
  "postgres"
  "redis"
)

echo "[journald-to-loki] Memulai pengumpulan log ke $LOG_DIR"
echo "[journald-to-loki] Service: ${SERVICES[*]}"

# Jalankan journalctl follow untuk setiap service secara paralel
pids=()
for svc in "${SERVICES[@]}"; do
  container_name="flashsale-${svc}"
  log_file="$LOG_DIR/${svc}.log"

  journalctl \
    CONTAINER_NAME="$container_name" \
    -f \
    -n 100 \
    --output=short-precise \
    2>/dev/null >> "$log_file" &

  pids+=($!)
  echo "[journald-to-loki] Memantau: $container_name -> $log_file (PID: $!)"
done

echo "[journald-to-loki] Semua proses berjalan. Tekan Ctrl+C untuk berhenti."

# Cleanup saat keluar
trap 'echo "[journald-to-loki] Menghentikan..."; kill "${pids[@]}" 2>/dev/null; exit 0' SIGINT SIGTERM

# Tunggu selamanya
wait
