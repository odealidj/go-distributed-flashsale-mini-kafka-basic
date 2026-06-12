#!/bin/bash
# =======================================================
# Script: Jalankan Semua Skenario Performance Test
# Urutan: Thundering Herd → Idempotency → No-Oversell
# (Soak test dijalankan manual karena durasi panjang)
# =======================================================

set -e

PRODUCT_ID="${PRODUCT_ID:-product-flashsale-001}"
INITIAL_STOCK="${INITIAL_STOCK:-100}"
BASE_URL="${BASE_URL:-http://localhost:18081}"
DIRECT_BASE_URL="${DIRECT_BASE_URL:-http://localhost:18000}"

mkdir -p results

echo "============================================="
echo "  FLASH SALE PERFORMANCE TEST SUITE"
echo "============================================="
echo "  BASE_URL        : $BASE_URL"
echo "  DIRECT_BASE_URL : $DIRECT_BASE_URL"
echo "  PRODUCT_ID      : $PRODUCT_ID"
echo "  INITIAL_STOCK   : $INITIAL_STOCK"
echo "============================================="
echo ""

# ── Skenario 1: Thundering Herd ──
echo "🌊 [1/3] Thundering Herd Test..."
echo "   Setup stok = $INITIAL_STOCK"
bash setup_stock.sh

k6 run \
  --env BASE_URL="$BASE_URL" \
  --env PRODUCT_ID="$PRODUCT_ID" \
  --out json=results/thundering_herd.json \
  k6/01_thundering_herd.js

echo ""

# ── Skenario 2: Idempotency ──
echo "🔄 [2/3] Idempotency Test (Menggunakan DIRECT_BASE_URL untuk bypass rate limiter)..."
echo "   Setup stok = 999999 (unlimited agar fokus pada idempotency)"
INITIAL_STOCK_BACKUP=$INITIAL_STOCK
INITIAL_STOCK=999999 bash setup_stock.sh

k6 run \
  --env BASE_URL="$DIRECT_BASE_URL" \
  --env PRODUCT_ID="$PRODUCT_ID" \
  --out json=results/idempotency.json \
  k6/02_idempotency_test.js

echo ""

# ── Skenario 3: No-Oversell ──
echo "✅ [3/3] No-Oversell Verification Test..."
echo "   Setup stok = $INITIAL_STOCK_BACKUP"
INITIAL_STOCK=$INITIAL_STOCK_BACKUP bash setup_stock.sh

k6 run \
  --env BASE_URL="$BASE_URL" \
  --env PRODUCT_ID="$PRODUCT_ID" \
  --env INITIAL_STOCK="$INITIAL_STOCK_BACKUP" \
  --out json=results/no_oversell.json \
  k6/04_no_oversell.js

echo ""
echo "============================================="
echo "  ✅ SEMUA TEST SELESAI"
echo "  Hasil disimpan di folder: results/"
echo "  Soak test (30 menit): jalankan manual:"
echo "  k6 run k6/03_soak_test.js"
echo "============================================="
