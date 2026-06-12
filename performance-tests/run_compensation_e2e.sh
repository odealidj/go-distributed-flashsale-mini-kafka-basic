#!/bin/bash
# =========================================================================
# Script: Otomatisasi Pengujian E2E Saga Compensation (Saga Rollback)
# =========================================================================

set -e

# Warna output terminal
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

BASE_URL="http://localhost:18081"
PRODUCT_ID="prod_1"
INITIAL_STOCK=10

echo -e "${BLUE}=================================================================${NC}"
echo -e "${BLUE}         SAGA COMPENSATION E2E AUTOMATED VERIFICATION           ${NC}"
echo -e "${BLUE}=================================================================${NC}"
echo -e "Target API Gateway  : ${CYAN}${BASE_URL}${NC}"
echo -e "Target Product ID   : ${CYAN}${PRODUCT_ID}${NC}"
echo -e "Initial Stock Level : ${CYAN}${INITIAL_STOCK}${NC}"
echo -e "${BLUE}-----------------------------------------------------------------${NC}"
echo ""

# ─────────────────────────────────────────────────────────────────
# 1. SETUP STOK AWAL DI REDIS
# ─────────────────────────────────────────────────────────────────
echo -e "${YELLOW}[Fase 1] Menyiapkan Stok Awal di Redis...${NC}"
docker exec -i flashsale-redis redis-cli FLUSHALL > /dev/null
docker exec -i flashsale-redis redis-cli SET "stock:${PRODUCT_ID}" "${INITIAL_STOCK}" > /dev/null
echo -e "  ✅ Redis stock:${PRODUCT_ID} diset ke ${GREEN}${INITIAL_STOCK}${NC}"
echo ""

# ─────────────────────────────────────────────────────────────────
# 2. KIRIM CHECKOUT ASINKRON VIA HTTP API GATEWAY
# ─────────────────────────────────────────────────────────────────
USER_ID="user-saga-auto-$RANDOM"
echo -e "${YELLOW}[Fase 2] Mengirim Checkout Asinkron...${NC}"
echo -e "  Generated User ID: ${CYAN}${USER_ID}${NC}"

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${BASE_URL}/api/v1/checkout" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${USER_ID}" \
  -d "{\"product_id\": \"${PRODUCT_ID}\"}")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
JSON_BODY=$(echo "$RESPONSE" | sed '$d')

echo -e "  HTTP Status Response: ${GREEN}${HTTP_CODE}${NC}"
if [ "$HTTP_CODE" -ne 202 ]; then
  echo -e "  ${RED}❌ Gagal melakukan checkout! HTTP status bukan 202.${NC}"
  echo -e "  Response Body: $JSON_BODY"
  exit 1
fi

EVENT_ID=$(echo "$JSON_BODY" | jq -r '.meta.event_id')
MESSAGE=$(echo "$JSON_BODY" | jq -r '.meta.message')

echo -e "  ✅ Response Event ID  : ${GREEN}${EVENT_ID}${NC}"
echo -e "  ✅ Response Message   : ${GREEN}${MESSAGE}${NC}"
echo ""

# ─────────────────────────────────────────────────────────────────
# 3. VERIFIKASI PENGURANGAN STOK DI REDIS (SINKRON)
# ─────────────────────────────────────────────────────────────────
echo -e "${YELLOW}[Fase 3] Verifikasi Pengurangan Stok Redis...${NC}"
STOCK_AFTER_CHECKOUT=$(docker exec -i flashsale-redis redis-cli GET "stock:${PRODUCT_ID}" | tr -d '\r')
echo -e "  Stok Redis Sekarang  : ${GREEN}${STOCK_AFTER_CHECKOUT}${NC} (Seharusnya: $((INITIAL_STOCK - 1)))"

if [ "$STOCK_AFTER_CHECKOUT" -eq $((INITIAL_STOCK - 1)) ]; then
  echo -e "  ✅ Asersi Stok Redis: ${GREEN}SUKSES (Stok terpotong 1 secara atomik)${NC}"
else
  echo -e "  ${RED}❌ Asersi Stok Redis: GAGAL (Stok tidak terpotong dengan benar)${NC}"
  exit 1
fi
echo ""

# ─────────────────────────────────────────────────────────────────
# 4. AMBIL ORDER ID DARI POSTGRESQL (ASINKRON - KAFKA CONSUMER)
# ─────────────────────────────────────────────────────────────────
echo -e "${YELLOW}[Fase 4] Mengambil Order ID dari Database Postgres (db_order)...${NC}"
echo -e "  Menunggu Kafka memproses pembuatan order (maks 20 detik)..."
ORDER_ID=""
for i in {1..20}; do
  ORDER_ID=$(docker exec -i flashsale-postgres psql -U root -d db_order -t -A -c "SELECT id FROM orders WHERE user_id = '${USER_ID}';" | tr -d '\r')
  if [ -n "$ORDER_ID" ]; then
    break
  fi
  sleep 1
done

if [ -z "$ORDER_ID" ]; then
  echo -e "  ${RED}❌ Gagal mengambil Order ID! Kafka Consumer di Order Service mungkin terhambat.${NC}"
  exit 1
fi

ORDER_STATUS_BEFORE=$(docker exec -i flashsale-postgres psql -U root -d db_order -t -A -c "SELECT status FROM orders WHERE id = '${ORDER_ID}';" | tr -d '\r')
echo -e "  ✅ Ditemukan Order ID : ${GREEN}${ORDER_ID}${NC}"
echo -e "  ✅ Status Order Awal  : ${GREEN}${ORDER_STATUS_BEFORE}${NC} (Seharusnya: PENDING)"

if [ "$ORDER_STATUS_BEFORE" != "PENDING" ]; then
  echo -e "  ${RED}❌ Asersi Status Order Awal: GAGAL (Status bukan PENDING)${NC}"
  exit 1
fi
echo ""

# ─────────────────────────────────────────────────────────────────
# 5. KIRIM PAYLOAD GAGAL BAYAR UNTUK PEMICU ROLLBACK SAGA
# ─────────────────────────────────────────────────────────────────
echo -e "${YELLOW}[Fase 5] Mengirim Pembayaran Gagal (Jumlah berakhiran angka 4)...${NC}"
# Kita gunakan nominal 150004 (sengaja dirancang agar ditolak bank/sistem payment)
PAY_PAYLOAD="{\"order_id\": \"${ORDER_ID}\", \"amount\": 150004}"

PAY_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${BASE_URL}/api/v1/pay" \
  -H "Content-Type: application/json" \
  -d "$PAY_PAYLOAD")

PAY_HTTP_CODE=$(echo "$PAY_RESPONSE" | tail -n1)
PAY_JSON_BODY=$(echo "$PAY_RESPONSE" | sed '$d')

echo -e "  HTTP Status Response: ${GREEN}${PAY_HTTP_CODE}${NC}"
# Service payment akan memproses asinkron atau sinkron, mengembalikan error 500/400 jika payment gagal secara bisnis
if [ "$PAY_HTTP_CODE" -ne 200 ] && [ "$PAY_HTTP_CODE" -ne 202 ] && [ "$PAY_HTTP_CODE" -ne 500 ]; then
  echo -e "  ${RED}❌ Gagal mengirimkan request pembayaran! Status HTTP: ${PAY_HTTP_CODE}${NC}"
  echo -e "  Response: $PAY_JSON_BODY"
  exit 1
fi

PAY_STATUS=$(echo "$PAY_JSON_BODY" | jq -r '.meta.message')
echo -e "  ✅ Respon Transaksi : ${GREEN}${PAY_STATUS}${NC}"
echo ""

# ─────────────────────────────────────────────────────────────────
# 6. VERIFIKASI SAGA ROLLBACK (POSTGRESQL & REDIS)
# ─────────────────────────────────────────────────────────────────
echo -e "${YELLOW}[Fase 6] Memverifikasi Kompensasi Saga (Saga Rollback)...${NC}"
echo -e "  Menunggu Kafka memproses kegagalan & me-refund stok (maks 20 detik)..."
for i in {1..20}; do
  ORDER_STATUS_AFTER=$(docker exec -i flashsale-postgres psql -U root -d db_order -t -A -c "SELECT status FROM orders WHERE id = '${ORDER_ID}';" | tr -d '\r')
  STOCK_AFTER_ROLLBACK=$(docker exec -i flashsale-redis redis-cli GET "stock:${PRODUCT_ID}" | tr -d '\r')
  if [ "$ORDER_STATUS_AFTER" == "CANCELLED" ]; then
    break
  fi
  sleep 1
done

echo -e "  Status Order Akhir   : ${GREEN}${ORDER_STATUS_AFTER}${NC} (Seharusnya: CANCELLED)"
echo -e "  Stok Redis Akhir     : ${GREEN}${STOCK_AFTER_ROLLBACK}${NC} (Seharusnya: ${INITIAL_STOCK})"
echo ""

# ASERSi FINAL
FAIL=0
if [ "$ORDER_STATUS_AFTER" == "CANCELLED" ]; then
  echo -e "  ✅ Asersi Status Order Rollback: ${GREEN}LULUS (Order berhasil dibatalkan)${NC}"
else
  echo -e "  ❌ Asersi Status Order Rollback: ${RED}GAGAL (Order tetap ${ORDER_STATUS_AFTER})${NC}"
  FAIL=1
fi

if [ "$STOCK_AFTER_ROLLBACK" -eq "$INITIAL_STOCK" ]; then
  echo -e "  ✅ Asersi Refund Stok Redis: ${GREEN}LULUS (Stok dikembalikan utuh ke ${INITIAL_STOCK})${NC}"
else
  echo -e "  ❌ Asersi Refund Stok Redis: ${RED}GAGAL (Stok tertahan di ${STOCK_AFTER_ROLLBACK})${NC}"
  FAIL=1
fi

echo ""
echo -e "${BLUE}=================================================================${NC}"
if [ "$FAIL" -eq 0 ]; then
  echo -e "${GREEN}      🎉 SEMUA ASERSI SAGA COMPENSATION E2E LULUS 100%!         ${NC}"
  echo -e "${BLUE}=================================================================${NC}"
  exit 0
else
  echo -e "${RED}      🚨 PENGUJIAN SAGA COMPENSATION E2E GAGAL!                 ${NC}"
  echo -e "${BLUE}=================================================================${NC}"
  exit 1
fi
