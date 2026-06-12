import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';
import { BASE_URL, PRODUCT_ID, registerAndGetToken, IS_NGINX } from './config.js';

const TARGET_VUS = __ENV.VUS ? parseInt(__ENV.VUS) : 500;
const HOLD_DURATION = __ENV.DURATION || '30s';

export const options = {
  tags: { testid: 'load-raw-checkout-polling' },
  scenarios: {
    thundering_herd: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '5s', target: TARGET_VUS }, // Lonjakan cepat
        { duration: HOLD_DURATION, target: TARGET_VUS }, // Bertahan di puncak
        { duration: '5s', target: 0 }, // Cool down
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    'http_req_duration': ['p(95)<1500'], // Ekspektasi polling butuh waktu
  },
};

export function setup() {
  // Mendapatkan satu JWT valid yang mensimulasikan user yang sudah login sebelum flash sale dimulai
  const token = registerAndGetToken();
  return { token };
}

export default function (data) {
  const userToken = data.token;
  const idempotencyKey = uuidv4();
  const payload = JSON.stringify({ product_id: PRODUCT_ID });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${userToken}`,
      'X-Idempotency-Key': idempotencyKey,
    },
  };

  // 1. Tembak Checkout Async (Raw)
  const checkoutRes = http.post(`${BASE_URL}/api/v1/checkout`, payload, params);
  
  const isAccepted = check(checkoutRes, {
    'checkout direspon 202, 409 (Stok Habis), atau 429 (Rate Limit)': (r) => {
        if (IS_NGINX && r.status === 429) return true;
        return [202, 409].includes(r.status);
    },
  });

  // Jika gagal (5xx, 429) atau stok habis (409), tidak usah lanjut polling
  if (checkoutRes.status !== 202) {
    sleep(0.1);
    return;
  }

  const orderID = checkoutRes.json('data.order_id');
  if (!orderID) {
    return;
  }

  // 2. Short-Polling ke API GetOrder hingga order berhasil terbuat di DB
  let orderStatus = "UNKNOWN";
  let maxRetries = 10;
  
  while (orderStatus === "UNKNOWN" && maxRetries > 0) {
    sleep(0.5); // Poll setiap 500ms
    
    const getOrderRes = http.get(`${BASE_URL}/api/v1/orders/${orderID}`, params);
    if (getOrderRes.status === 200) {
      orderStatus = getOrderRes.json('data.status') || "UNKNOWN";
    }
    maxRetries--;
  }

  check(orderStatus, {
    'order selesai diproses (ada di DB, status PENDING)': (status) => status === "PENDING",
  });

  // 3. Jika berhasil membuat order (PENDING/PAID/etc), kita simulasikan eksekusi Pembayaran
  // Di dunia nyata, ini dilakukan user setelah melihat layar status
  const payPayload = JSON.stringify({
    order_id: orderID,
    amount: 150000, // asumsikan selalu sukses kecuali berakhiran digit 4
  });

  const payRes = http.post(`${BASE_URL}/api/v1/pay`, payPayload, params);
  check(payRes, {
    'payment direspon 200 OK atau 429 (Rate Limit)': (r) => {
        if (IS_NGINX && r.status === 429) return true;
        return r.status === 200;
    },
  });

  sleep(0.1);
}
