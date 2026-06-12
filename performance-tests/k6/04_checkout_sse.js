import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';
import { BASE_URL, PRODUCT_ID, registerAndGetToken, IS_NGINX } from './config.js';

const TARGET_VUS = __ENV.VUS ? parseInt(__ENV.VUS) : 500;
const HOLD_DURATION = __ENV.DURATION || '30s';

export const options = {
  tags: { testid: 'load-sse' },
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
    'http_req_duration': ['p(95)<3000'], // SSE sengaja menahan koneksi
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
    timeout: '15s', // SSE bisa makan waktu sedikit lebih lama karena flush stream berulang
  };

  // 1. Tembak Checkout dengan tipe SSE
  // K6 http.post akan menahan eksekusi hingga stream selesai (koneksi ditutup oleh server API Gateway).
  const checkoutRes = http.post(`${BASE_URL}/api/v1/checkout/sse`, payload, params);
  
  const isOk = check(checkoutRes, {
    'checkout direspon 200, 202, 409, atau 429': (r) => {
        if (IS_NGINX && r.status === 429) return true;
        return [200, 202, 409].includes(r.status);
    },
    'jika 200, body memuat "data:"': (r) => r.status !== 200 || (r.body && r.body.includes('data:')),
    'jika 200, body memuat success': (r) => r.status !== 200 || (r.body && r.body.includes('"message":"success"')),
  });

  // Hanya memproses order & pay jika order sukses dibuat
  if (checkoutRes.status !== 200) {
    sleep(0.1);
    return;
  }

  // SSE response is an event stream string, we need to extract the order ID from the final data chunk
  // Format: data: {"meta":{...},"data":{"order_id":"idemp-..."}}
  let orderID = "";
  try {
    const lines = checkoutRes.body.split("\n");
    for (let i = 0; i < lines.length; i++) {
        if (lines[i].startsWith("data:")) {
            const jsonStr = lines[i].substring(5).trim();
            const parsed = JSON.parse(jsonStr);
            if (parsed.data && parsed.data.order_id) {
                orderID = parsed.data.order_id;
            }
        }
    }
  } catch(e) {}

  if (!orderID) {
     return;
  }

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
