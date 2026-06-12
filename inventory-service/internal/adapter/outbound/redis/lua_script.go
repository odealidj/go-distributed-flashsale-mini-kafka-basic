package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go-flashsale-mini-kafka-basic/inventory-service/internal/application/port"
)

type redisPort struct {
	client *redis.Client
}

func NewRedisPort(client *redis.Client) port.RedisPort {
	return &redisPort{client: client}
}

// ReserveStockScript digunakan untuk mengecek stok, mengurangi stok, dan menyimpan
// idempotency key secara atomic dalam 1 operasi Redis.
//
// Perubahan dari versi sebelumnya:
//   - Idempotency key sekarang menyimpan nilai "{productID}:{quantity}" alih-alih "1".
//   - Hal ini memungkinkan ReconciliationJob membaca metadata reservasi yang "bocor"
//     (terpotong di Redis tapi gagal masuk ke Postgres Outbox).
const ReserveStockScript = `
local stock_key = KEYS[1]
local idemp_key = KEYS[2]
local amount    = tonumber(ARGV[1])
local meta      = ARGV[2]   -- "{productID}:{quantity}" untuk reconciliation

-- Cek Idempotency: jika sudah ada, tolak (duplicate request)
if redis.call("EXISTS", idemp_key) == 1 then
    return 0
end

-- Cek Stok
local current_stock = tonumber(redis.call("GET", stock_key))
if current_stock == nil or current_stock < amount then
    return 0  -- Stok habis atau tidak cukup
end

-- Potong Stok
redis.call("DECRBY", stock_key, amount)

-- Simpan idempotency key dengan metadata: "{productID}:{quantity}"
-- TTL 2 jam (7200s) memberi ReconciliationJob waktu yang cukup untuk mendeteksi
-- reservasi yang "bocor" (normalnya Outbox diproses dalam detik/menit).
redis.call("SET", idemp_key, meta, "EX", 7200)

return 1
`

// RefundStockScript mengembalikan stok dan menghapus idempotency key secara atomic.
const RefundStockScript = `
local stock_key = KEYS[1]
local idemp_key = KEYS[2]
local amount    = tonumber(ARGV[1])

-- Kembalikan Stok
redis.call("INCRBY", stock_key, amount)
-- Hapus idempotency_key agar user bisa beli lagi jika promo masih berlangsung
redis.call("DEL", idemp_key)

return 1
`

var reserveLuaScript = redis.NewScript(ReserveStockScript)

func (r *redisPort) ReserveStock(ctx context.Context, productID string, eventID string, quantity int) (bool, error) {
	stockKey := fmt.Sprintf("stock:%s", productID)
	idempKey := fmt.Sprintf("reserve_idemp:%s", eventID)
	// meta yang akan disimpan: "productID:quantity" untuk dibaca ReconciliationJob
	meta := fmt.Sprintf("%s:%d", productID, quantity)

	res, err := reserveLuaScript.Run(ctx, r.client, []string{stockKey, idempKey}, quantity, meta).Int()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}

	return res == 1, nil
}

var refundLuaScript = redis.NewScript(RefundStockScript)

func (r *redisPort) RefundStock(ctx context.Context, productID string, eventID string, quantity int) (bool, error) {
	stockKey := fmt.Sprintf("stock:%s", productID)
	idempKey := fmt.Sprintf("reserve_idemp:%s", eventID)

	res, err := refundLuaScript.Run(ctx, r.client, []string{stockKey, idempKey}, quantity).Int()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}

	return res == 1, nil
}

// GetLeakedReservations mencari semua idempotency key yang usianya sudah melewati
// gracePeriodSecs detik. Key-key ini berpotensi merupakan reservasi yang "bocor":
// stok sudah terpotong di Redis tapi event gagal masuk ke Postgres Outbox.
// Mengembalikan map dari eventID -> metadata ("productID:quantity").
func (r *redisPort) GetLeakedReservations(ctx context.Context, gracePeriodSecs int) (map[string]string, error) {
	pattern := "reserve_idemp:*"
	var cursor uint64
	result := make(map[string]string)

	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("gagal scan redis keys: %w", err)
		}

		for _, key := range keys {
			// Cek sisa TTL (dalam detik). Key yang memiliki TTL mendekati batas max TTL
			// adalah reservasi "lama" yang berpotensi bocor.
			// TTL max = 7200s. Jika TTL < (7200 - gracePeriodSecs) → sudah melewati grace period.
			ttl, err := r.client.TTL(ctx, key).Result()
			if err != nil {
				continue
			}
			remainingTTL := int(ttl.Seconds())
			// Key sudah melewati grace period jika remaining TTL < maxTTL - gracePeriodSecs
			const maxTTLSecs = 7200
			if remainingTTL > 0 && remainingTTL < (maxTTLSecs-gracePeriodSecs) {
				// Ambil metadata yang disimpan: "productID:quantity"
				meta, err := r.client.Get(ctx, key).Result()
				if err != nil {
					continue
				}
				// Ekstrak eventID dari key "reserve_idemp:{eventID}"
				eventID := key[len("reserve_idemp:"):]
				result[eventID] = meta
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return result, nil
}
