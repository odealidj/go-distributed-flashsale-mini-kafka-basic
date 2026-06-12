import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend, Rate } from 'k6/metrics';
import { BASE_URL, PRODUCT_ID, registerAndGetToken } from './config.js';

/**
 * Skenario 06: Full Flash Sale Spike Test (Prod Infra)
 *
 * Skenario ini KHUSUS dibuat untuk relevansi dengan spesifikasi produksi:
 * - Redis Sentinel (HA)
 * - Kafka 1 Node, 10 Partitions (Concurrency di Order Service)
 * - Hardware Host: Ryzen 5900HS, 13GB RAM (Kapasitas VUs dibatasi agar tidak crash)
 *
 * Perbedaan dengan skenario lain:
 * 1. Menggunakan "ramping-arrival-rate" untuk mensimulasikan SPIKE (lonjakan instan)
 *    hingga ratusan/ribuan request per detik secara presisi, lebih ramah CPU laptop
 *    dibandingkan memaksakan 5000 VU "per-vu-iterations".
 * 2. Menguji end-to-end: Checkout -> Kafka (10 partisi) -> Polling Order -> Pay.
 * 3. Membuktikan Kafka 10 partisi dapat mempercepat pembentukan order.
 *
 * JALANKAN DENGAN TARGET BACKEND (Bypass Nginx IP Rate Limiter):
 * k6 run -e TARGET=backend performance-tests/k6/06_flashsale_spike_prod.js
 */

const successfulCheckouts = new Counter('spike_checkout_success');
const failedCheckouts = new Counter('spike_checkout_failed');
const successfulPayments = new Counter('spike_payment_success');
const orderCreationLatency = new Trend('spike_order_creation_latency_ms');
const errorRate = new Rate('spike_system_errors');

export const options = {
  tags: { testid: 'flashsale-spike-prod' },
  scenarios: {
    flashsale_spike: {
      executor: 'ramping-arrival-rate',
      startRate: 0,
      timeUnit: '1s',
      preAllocatedVUs: 300, // Alokasi awal goroutine di k6 (Ramah untuk Ryzen 5900HS)
      maxVUs: 1500,         // Maksimal VU yang disiapkan jika sistem backend melambat
      stages: [
        { duration: '10s', target: 500 }, // Spike drastis dari 0 ke 500 request/detik
        { duration: '15s', target: 500 }, // Tahan lonjakan selama 15 detik
        { duration: '10s', target: 0 },   // Trafik mereda
      ],
    },
  },
  thresholds: {
    // 95% checkout harus selesai di bawah 1.5 detik saat spike
    http_req_duration: ['p(95)<1500'],
    // Order harus terbentuk via Kafka 10 partisi rata-rata di bawah 3 detik
    spike_order_creation_latency_ms: ['p(95)<3000'],
    // Toleransi error sistem (bukan 409 stok habis)
    spike_system_errors: ['rate<0.05'], 
  },
};

export function setup() {
  // Generate 1 token untuk load test. 
  // Di prod, verifikasi token adalah stateless (RS256 Public Key) di Gateway,
  // sehingga menggunakan 1 token berulang-ulang memiliki beban CPU yang identik
  // dengan menggunakan 5000 token berbeda.
  const token = registerAndGetToken();
  return { token };
}

export default function (data) {
  const token = data.token;
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
    // UUID unik per request mencegah Layer 1 Idempotency di Redis menganggap ini duplikat
    'X-Idempotency-Key': `spike-${__VU}-${__ITER}-${Date.now()}`
  };

  // 1. FASE CHECKOUT (Hit Redis Sentinel Lua Script)
  const checkoutPayload = JSON.stringify({ product_id: PRODUCT_ID, quantity: 1 });
  const checkoutRes = http.post(`${BASE_URL}/api/v1/checkout`, checkoutPayload, { headers });

  const isCheckoutSuccess = check(checkoutRes, {
    'Checkout 202 Accepted': (r) => r.status === 202,
  });

  if (isCheckoutSuccess) {
    successfulCheckouts.add(1);
    
    // Ekstrak order_id dari payload
    let orderId = "";
    try {
      const body = JSON.parse(checkoutRes.body);
      orderId = body.data.order_id;
    } catch(e) {}

    if (orderId) {
      // 2. FASE POLLING & KAFKA CONSUMPTION
      // Menguji kecepatan Kafka 1 node 10 partitions + Relay Worker
      let isPending = false;
      let attempts = 0;
      const startPoll = Date.now();
      
      // Polling maks 10x (5 detik) untuk menunggu consumer bekerja
      while (!isPending && attempts < 10) {
        sleep(0.5); 
        const orderRes = http.get(`${BASE_URL}/api/v1/orders/${orderId}`, { headers });
        
        if (orderRes.status === 200) {
          try {
             const ordBody = JSON.parse(orderRes.body);
             if (ordBody.data && ordBody.data.status === 'PENDING') {
               isPending = true;
               orderCreationLatency.add(Date.now() - startPoll);
             }
          } catch(e) {}
        }
        attempts++;
      }

      // 3. FASE PEMBAYARAN
      // Menguji Payment Service dan Saga Flow selanjutnya
      if (isPending) {
        const payPayload = JSON.stringify({ order_id: orderId, amount: 150000 });
        const payRes = http.post(`${BASE_URL}/api/v1/pay`, payPayload, { headers });
        
        if (payRes.status === 200) {
           successfulPayments.add(1);
        } else {
           errorRate.add(1);
        }
      } else {
        errorRate.add(1); // Gagal mendapatkan status PENDING (Kafka terlalu lambat/error)
      }
    }
  } else if (checkoutRes.status === 409 || checkoutRes.status === 429) {
    failedCheckouts.add(1); // Stok habis atau Rate Limited
  } else {
    errorRate.add(1);
  }
}

export function handleSummary(data) {
  const successCheckout = data.metrics['spike_checkout_success']?.values?.count || 0;
  const successPayment = data.metrics['spike_payment_success']?.values?.count || 0;
  const orderLatency = Math.round(data.metrics['spike_order_creation_latency_ms']?.values?.['p(95)'] || 0);
  
  const report = `
╔══════════════════════════════════════════════════════════╗
║        HASIL SPIKE TEST (FLASH SALE PROD INFRA)          ║
╠══════════════════════════════════════════════════════════╣
║ Hardware/Infra : Ryzen 5900HS / Redis HA / Kafka 1x10    ║
║ Target Rate    : 500 RPS (Spike Instan)                  ║
╠══════════════════════════════════════════════════════════╣
║ 🛒 Checkout Sukses (Redis)  : ${String(successCheckout).padStart(5)} request             ║
║ ⚡ P95 Latency Order (Kafka): ${String(orderLatency).padStart(5)} ms                  ║
║ 💳 Payment Sukses           : ${String(successPayment).padStart(5)} transaksi           ║
╚══════════════════════════════════════════════════════════╝
`;

  console.log(report);

  return {
    'stdout': report,
    './results/spike_prod_summary.json': JSON.stringify(data, null, 2),
  };
}
