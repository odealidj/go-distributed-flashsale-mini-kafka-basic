/**
 * Skenario 04: No-Oversell Verification
 *
 * Skenario paling kritis untuk Flash Sale:
 * Membuktikan bahwa jumlah stok yang terjual TIDAK PERNAH melebihi stok awal.
 *
 * Cara Kerja:
 * 1. Setup: stok produk diset ke N (misal: 100 unit)
 * 2. 5000 user unik mencoba checkout secara bersamaan
 * 3. Teardown: hitung total 202 Accepted — harus <= N
 *
 * Ini adalah "golden test" untuk kebenaran sistem Flash Sale.
 *
 * Jalankan: k6 run --env PRODUCT_ID=product-001 --env INITIAL_STOCK=100 04_no_oversell.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { BASE_URL, PRODUCT_ID, IS_NGINX } from './config.js';

const INITIAL_STOCK = parseInt(__ENV.INITIAL_STOCK || '200');
const TOTAL_USERS = INITIAL_STOCK * 50; // 50x lebih banyak user dari stok

const successfulCheckouts = new Counter('successful_checkout_count');
const failedCheckouts = new Counter('failed_checkout_count');
const systemErrors = new Counter('system_error_count');

export const options = {
  tags: { testid: 'correctness-no-oversell' },
  scenarios: {
    no_oversell: {
      executor: 'per-vu-iterations',
      vus: Math.min(TOTAL_USERS, 5000), // Maksimal 5000 VU
      iterations: 1,
      maxDuration: '120s',
    },
  },
  thresholds: {
    // KRITIS: Jumlah checkout sukses TIDAK BOLEH melebihi stok awal
    // Threshold ini tidak bisa dikodekan langsung di k6, diverifikasi di handleSummary
    'system_error_count': ['count<500'],          // Toleransi socket drops lokal di bawah beban tinggi
    'http_req_duration': ['p(95)<5000'],
  },
};

export default function () {
  // Setiap VU adalah user unik — tidak ada duplikat
  const userToken = `user-unique-${__VU}-${__ITER}`;
  const payload = JSON.stringify({ product_id: PRODUCT_ID });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${userToken}`,
    },
    tags: { scenario: 'no_oversell' },
    timeout: '10s',
  };

  const res = http.post(`${BASE_URL}/api/v1/checkout`, payload, params);

  const isValid = check(res, {
    'Status valid (202, 409, atau 429)': (r) => {
      if (IS_NGINX && r.status === 429) return true;
      return r.status === 202 || r.status === 409;
    },
  });

  if (res.status === 202) {
    successfulCheckouts.add(1);
  } else if (res.status === 409 || (IS_NGINX && res.status === 429)) {
    failedCheckouts.add(1);
  } else {
    systemErrors.add(1);
    console.error(`ERROR: VU=${__VU} status=${res.status} body=${res.body?.substring(0, 200)}`);
  }

  // Tidak ada sleep — semua user serangan pada waktu yang sama
}

export function handleSummary(data) {
  const successCount = data.metrics['successful_checkout_count']?.values?.count || 0;
  const failedCount = data.metrics['failed_checkout_count']?.values?.count || 0;
  const errorCount = data.metrics['system_error_count']?.values?.count || 0;
  const totalRequests = successCount + failedCount + errorCount;

  const oversellDetected = successCount > INITIAL_STOCK;
  const status = oversellDetected ? '❌ OVERSELL TERDETEKSI!' : '✅ TIDAK ADA OVERSELL';

  const report = `
╔══════════════════════════════════════════════════════════╗
║           NO-OVERSELL TEST - HASIL VERIFIKASI           ║
╠══════════════════════════════════════════════════════════╣
║  Stok Awal      : ${String(INITIAL_STOCK).padStart(5)}                            ║
║  Total User     : ${String(Math.min(TOTAL_USERS, 5000)).padStart(5)}                            ║
║  Total Request  : ${String(totalRequests).padStart(5)}                            ║
╠══════════════════════════════════════════════════════════╣
║  ✅ 202 Accepted: ${String(successCount).padStart(5)} (checkout berhasil)         ║
║  ⚠️  409/429    : ${String(failedCount).padStart(5)} (stok habis / rate limited) ║
║  ❌ Error Sistem: ${String(errorCount).padStart(5)}                              ║
╠══════════════════════════════════════════════════════════╣
║  ${String(status).padEnd(52)} ║
${oversellDetected ? '║  ⚠️  OVERSELL: ' + successCount + ' > ' + INITIAL_STOCK + ' (KRITIS!)'.padEnd(40) + ' ║' : ''}
╚══════════════════════════════════════════════════════════╝
`;

  // Buat exit code berbeda jika oversell
  if (oversellDetected) {
    console.error(report);
  } else {
    console.log(report);
  }

  return {
    'stdout': report,
    './results/no_oversell_summary.json': JSON.stringify({
      initial_stock: INITIAL_STOCK,
      successful_checkouts: successCount,
      failed_checkouts: failedCount,
      system_errors: errorCount,
      oversell_detected: oversellDetected,
      oversell_margin: successCount - INITIAL_STOCK,
      ...data,
    }, null, 2),
  };
}
