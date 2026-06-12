#!/bin/bash
set -e

echo "1. Register User..."
curl -s -X POST http://localhost:18000/api/v1/register -H "Content-Type: application/json" -d '{"username": "testuser", "password": "password123", "email": "test@example.com"}' | jq

echo -e "\n2. Login User..."
LOGIN_RESP=$(curl -s -X POST http://localhost:18000/api/v1/login -H "Content-Type: application/json" -d '{"username": "testuser", "password": "password123"}')
TOKEN=$(echo $LOGIN_RESP | jq -r .data.access_token)
echo "Token: $TOKEN"

echo -e "\n3. Checkout..."
IDEMP="idemp-test-$(date +%s)"
CHECKOUT_RESP=$(curl -s -X POST http://localhost:18000/api/v1/checkout -H "Authorization: Bearer $TOKEN" -H "X-Idempotency-Key: $IDEMP" -H "Content-Type: application/json" -d '{"product_id": "prod_1"}')
echo $CHECKOUT_RESP | jq

echo -e "\n4. Wait 2 seconds for Kafka processing..."
sleep 2

echo -e "\n5. Get Order..."
ORDER_RESP=$(curl -s -X GET http://localhost:18000/api/v1/orders/$IDEMP -H "Authorization: Bearer $TOKEN")
echo $ORDER_RESP | jq

echo -e "\n6. Pay Order..."
curl -s -X POST http://localhost:18000/api/v1/pay -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "{\"order_id\": \"$IDEMP\", \"amount\": 100000}" | jq
