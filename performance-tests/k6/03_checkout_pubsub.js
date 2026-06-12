import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';
import { BASE_URL, PRODUCT_ID, registerAndGetToken, IS_NGINX } from './config.js';

const TARGET_VUS = __ENV.VUS ? parseInt(__ENV.VUS) : 500;
const HOLD_DURATION = __ENV.DURATION || '30s';

export const options = {
  tags: { testid: 'load-pubsub' },
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
    'http_req_duration': ['p(95)<3000'], // PubSub sengaja menahan koneksi
  },
};

export function setup() {
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
    timeout: '10s', // Jangan putus sebelum PubSub server merespon
  };

  // 1. Tembak Checkout dengan tipe PubSub
  const checkoutRes = http.post(`${BASE_URL}/api/v1/checkout/pubsub`, payload, params);
  
  const isOk = check(checkoutRes, {
    'checkout direspon 200, 409 (Habis), atau 429 (Rate Limit)': (r) => {
        if (IS_NGINX && r.status === 429) return true;
        return [200, 409].includes(r.status);
    },
    'jika 200, status adalah PENDING': (r) => {
        if (r.status !== 200) return true; // abaikan jika bukan 200 OK
        try { return r.json('data.status') && r.json('data.status') === 'PENDING'; }
        catch (e) { return false; }
    }
  });

  // Hanya memproses order & pay jika order sukses dibuat (200 OK)
  if (checkoutRes.status !== 200) {
    sleep(0.1);
    return;
  }

  const orderID = checkoutRes.json('data.order_id');

  // 2. Simulasi eksekusi Pembayaran
  const payPayload = JSON.stringify({
    order_id: orderID,
    amount: 150000, 
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
