import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate } from 'k6/metrics';
import { BASE_URL, PRODUCT_ID, registerAndGetToken } from './config.js';

/**
 * Skenario 07: Breakpoint / Stress Test
 *
 * Menggunakan "ramping-arrival-rate" untuk mencari limit absolut
 * dari sistem (Gateway + Inventory + Redis Lua + Kafka Producer).
 * Beban akan naik secara perlahan dari 50 RPS hingga 3000 RPS.
 *
 * Menggunakan stok 1.000.000 agar sistem (Postgres & Kafka)
 * terus bekerja keras mengolah event, bukan sekadar membalas HTTP 409 (Stok Habis)
 * yang sangat ringan untuk CPU.
 *
 * JALANKAN DENGAN TARGET BACKEND (Bypass Nginx IP Rate Limiter):
 * make test-breakpoint
 */

const successfulCheckouts = new Counter('breakpoint_checkout_success');
const failedStock = new Counter('breakpoint_checkout_out_of_stock');
const errorRate = new Rate('breakpoint_system_errors');

export const options = {
  tags: { testid: 'flashsale-breakpoint' },
  scenarios: {
    find_limit: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 1000, 
      maxVUs: 5000,         
      stages: [
        { duration: '30s', target: 200 },  // Naik perlahan ke 200 RPS
        { duration: '30s', target: 500 },  // Naik ke 500 RPS
        { duration: '30s', target: 1000 }, // Naik ke 1000 RPS
        { duration: '30s', target: 2000 }, // Naik ke 2000 RPS
        { duration: '30s', target: 3000 }, // Naik ke 3000 RPS
      ],
    },
  },
  thresholds: {
    // Breakpoint: Error sistem tidak boleh lebih dari 5%. Jika lebih, tes akan dianggap gagal (limit tercapai)
    breakpoint_system_errors: ['rate<0.05'], 
    // Latency P95 harus di bawah 2 detik untuk checkout. Jika lewat, berarti bottleneck parah.
    http_req_duration: ['p(95)<2000'],
  },
};

export function setup() {
  const token = registerAndGetToken();
  return { token };
}

export default function (data) {
  const token = data.token;
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
    'X-Idempotency-Key': `break-${__VU}-${__ITER}-${Date.now()}`
  };

  const checkoutPayload = JSON.stringify({ product_id: PRODUCT_ID, quantity: 1 });
  const checkoutRes = http.post(`${BASE_URL}/api/v1/checkout`, checkoutPayload, { headers });

  const isSuccess = check(checkoutRes, {
    'Checkout 202 Accepted': (r) => r.status === 202,
  });

  if (isSuccess) {
    successfulCheckouts.add(1);
  } else if (checkoutRes.status === 409) {
    failedStock.add(1);
  } else if (checkoutRes.status === 429) {
    // Wajar jika test dilakukan lewat Nginx
  } else {
    errorRate.add(1);
  }
}

export function handleSummary(data) {
  const success = data.metrics['breakpoint_checkout_success']?.values?.count || 0;
  const outOfStock = data.metrics['breakpoint_checkout_out_of_stock']?.values?.count || 0;
  const errors = data.metrics['breakpoint_system_errors']?.values?.rate || 0;
  const p95Latency = Math.round(data.metrics['http_req_duration']?.values?.['p(95)'] || 0);
  const totalReq = data.metrics['http_reqs']?.values?.count || 0;
  
  const report = `
╔══════════════════════════════════════════════════════════╗
║              HASIL PENCARIAN BREAKPOINT                  ║
╠══════════════════════════════════════════════════════════╣
║ Total Request Terkirim      : ${String(totalReq).padStart(5)}                    ║
║ 🛒 Checkout Sukses (202)    : ${String(success).padStart(5)}                    ║
║ 🚫 Stok Habis (409)         : ${String(outOfStock).padStart(5)}                    ║
║ 💀 Error Sistem (500/Timeout): ${String((errors*100).toFixed(2)).padStart(5)} %                  ║
║ ⚡ P95 Latency API Checkout  : ${String(p95Latency).padStart(5)} ms                  ║
╚══════════════════════════════════════════════════════════╝
`;

  console.log(report);

  return {
    'stdout': report,
  };
}
