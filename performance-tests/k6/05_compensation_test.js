import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

export const options = {
  vus: 1,        // Gunakan 1 VU saja agar pengujian Saga fungsional stabil tanpa terkena Rate Limit NGINX
  iterations: 1, 
  tags: {
    testid: 'functional-compensation',
    test_type: 'compensation',
  },
};

const BASE_URL = 'http://localhost:18081'; // Nginx Proxy port dinamis lokal kita
const PRODUCT_ID = 'prod_1';

export default function () {
  // Pastikan ID unik per percobaan agar tidak bentrok idempotency
  const userID = `user-saga-${__VU}-${__ITER}-${uuidv4().substring(0, 8)}`;
  
  const checkoutPayload = JSON.stringify({
    product_id: PRODUCT_ID,
  });

  const checkoutParams = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${userID}`,
    },
  };

  // 1. Checkout
  const checkoutRes = http.post(`${BASE_URL}/api/v1/checkout`, checkoutPayload, checkoutParams);
  const isAccepted = check(checkoutRes, {
    'Checkout direspon 202 Accepted': (r) => r.status === 202,
    'Respon memiliki event_id': (r) => r.json('meta.event_id') !== undefined,
  });

  if (!isAccepted) return;
  const eventID = checkoutRes.json('meta.event_id');

  // Tunggu sejenak agar Kafka selesai memproses antrean PENDING
  sleep(3);

  // 2. Verifikasi status PENDING
  const getOrderRes = http.get(`${BASE_URL}/api/v1/orders/${eventID}`, { headers: checkoutParams.headers });
  check(getOrderRes, {
    'Order ditemukan': (r) => r.status === 200,
    'Status order PENDING': (r) => r.json('data.status') === 'PENDING',
  });

  if (getOrderRes.status !== 200) return;

  // 3. Simulasikan kegagalan pembayaran
  const payPayload = JSON.stringify({
    order_id: eventID,
    amount: 150004, // Akhiran 4 = trigger error simulasi payment
  });
  const payRes = http.post(`${BASE_URL}/api/v1/pay`, payPayload, checkoutParams);
  
  check(payRes, {
    'Pembayaran ditolak gateway (code=Unknown)': (r) => r.json('meta.message') !== undefined && r.json('meta.message').includes('payment gagal diproses'),
  });

  // 4. Tunggu proses kompensasi (Saga Rollback) via Kafka
  sleep(3);

  // 5. Verifikasi status order menjadi CANCELLED
  const getOrderCancelRes = http.get(`${BASE_URL}/api/v1/orders/${eventID}`, { headers: checkoutParams.headers });
  
  check(getOrderCancelRes, {
    'Status order berubah menjadi CANCELLED': (r) => r.json('data.status') === 'CANCELLED',
  });
}
