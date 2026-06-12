/**
 * Skenario 03: Soak Test - Uji Ketahanan Jangka Panjang
 *
 * Menjalankan load sedang selama 30 menit untuk mendeteksi:
 *   - Memory leak (goroutine yang tidak dibersihkan)
 *   - Connection pool exhaustion (DB/Redis)
 *   - Degradasi performa seiring waktu (latency P95 meningkat)
 *
 * Jalankan: k6 run --env PRODUCT_ID=product-001 03_soak_test.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate } from 'k6/metrics';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';
import { BASE_URL, PRODUCT_ID, registerAndGetToken, IS_NGINX } from './config.js';

const checkoutLatency = new Trend('soak_checkout_latency_ms', true);
const productListLatency = new Trend('soak_product_list_latency_ms', true);
const systemErrors = new Rate('system_errors');

export const options = {
  tags: { testid: 'soak-30m' },
  scenarios: {
    soak: {
      executor: 'constant-vus',
      vus: 300,          // Beban tinggi berkelanjutan untuk menguji Rate Limiting & CPU
      duration: '30m',   // Jangka panjang untuk deteksi memory leak
    },
  },
  thresholds: {
    // Latency tidak boleh meningkat lebih dari 2x dari awal ke akhir
    'soak_checkout_latency_ms': [
      'p(95)<1000',  // Tidak boleh melebihi 1 detik di P95 sepanjang test
      'p(99)<2000',
    ],
    'soak_product_list_latency_ms': ['p(95)<300'],
    'system_errors': ['rate<0.01'], // Kegagalan sistem murni (error/5xx) harus di bawah 1%
  },
};

export function setup() {
  const token = registerAndGetToken();
  return { token };
}

export default function (data) {
  const userToken = data.token;
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${userToken}`,
  };

  // Selingi antara list products dan checkout untuk mensimulasikan user flow nyata
  const scenario = Math.random();

  if (scenario < 0.7) {
    // 70% request: list products (read-heavy)
    const startTime = Date.now();
    const res = http.get(`${BASE_URL}/api/v1/products?page=1&per_page=20`, {
      headers,
      tags: { type: 'read' },
    });
    productListLatency.add(Date.now() - startTime);

    const isSuccess = check(res, {
      'Product list status valid': (r) => {
          if (IS_NGINX && r.status === 429) return true;
          return r.status === 200;
      },
    });
    systemErrors.add(!isSuccess);

    sleep(1 + Math.random() * 2); // User browsing
  } else {
    // 30% request: checkout (write-heavy)
    const startTime = Date.now();
    const res = http.post(
      `${BASE_URL}/api/v1/checkout`,
      JSON.stringify({ product_id: PRODUCT_ID }),
      { headers, tags: { type: 'write' } }
    );
    checkoutLatency.add(Date.now() - startTime);

    const isValid = check(res, {
      'Checkout status valid': (r) => {
          if (IS_NGINX && r.status === 429) return true;
          return [202, 409].includes(r.status);
      },
    });
    systemErrors.add(!isValid);

    sleep(0.5 + Math.random() * 1.5); // Jeda antar checkout
  }
}

export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
    './results/soak_test_summary.json': JSON.stringify(data, null, 2),
  };
}
