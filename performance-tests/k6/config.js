import http from 'k6/http';

// Konfigurasi global untuk semua skenario k6
export const TARGET = __ENV.TARGET || 'nginx';
export const IS_NGINX = TARGET === 'nginx';

// Di Real Prod, kita memukul Nginx Reverse Proxy (port 18081).
// Jika bypass (backend), kita memukul API Gateway langsung (port 18000).
export const BASE_URL = __ENV.BASE_URL || (IS_NGINX ? 'http://localhost:18081' : 'http://localhost:18000');

// Produk Flash Sale ID yang akan diuji (Sesuai seed_stock)
export const PRODUCT_ID = __ENV.PRODUCT_ID || 'prod_1';

// Mendaftarkan dan melakukan login user untuk mendapatkan REAL JWT Token.
// Ini dieksekusi di blok setup() K6, agar Gateway sungguhan melakukan RS256 Crypto Verification.
export function registerAndGetToken() {
  const username = `k6_user_${Date.now()}_${Math.floor(Math.random() * 100000)}`;
  const password = `password123`;
  const payload = JSON.stringify({ username, password });
  const params = { headers: { 'Content-Type': 'application/json' } };

  // 1. Register User Baru
  http.post(`${BASE_URL}/api/v1/register`, payload, params);

  // 2. Login User tersebut
  const loginRes = http.post(`${BASE_URL}/api/v1/login`, payload, params);

  try {
    const data = JSON.parse(loginRes.body);
    if (data && data.data && data.data.access_token) {
      return data.data.access_token;
    }
  } catch (e) {}

  console.error("GAGAL MENDAPATKAN TOKEN JWT SAAT SETUP:", loginRes.body);
  return "";
}

// Threshold standar: error rate < 1%, P95 < 500ms
export const DEFAULT_THRESHOLDS = {
  http_req_failed: ['rate<0.01'],
  http_req_duration: ['p(95)<500'],
};

// Tag untuk grouping di Grafana
export const TAGS = {
  service: 'api-gateway',
  scenario: 'flash-sale',
};
