#!/bin/bash
set -e

mkdir -p certs

echo "Generating RS256 Private Key..."
openssl genrsa -out certs/private.pem 2048

echo "Generating RS256 Public Key..."
openssl rsa -in certs/private.pem -pubout -out certs/public.pem

echo "Keys generated successfully in certs/ directory."
