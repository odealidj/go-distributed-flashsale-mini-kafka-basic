#!/bin/bash
set -e

echo "Membuat database logis terpisah..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE DATABASE db_product;
    CREATE DATABASE db_inventory;
    CREATE DATABASE db_order;
    CREATE DATABASE db_payment;
    CREATE DATABASE db_auth;
EOSQL

echo "Inisialisasi tabel untuk db_product..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "db_product" <<-EOSQL
    CREATE TABLE IF NOT EXISTS products (
        id VARCHAR(50) PRIMARY KEY,
        name VARCHAR(255) NOT NULL,
        original_price BIGINT NOT NULL,
        flash_sale_price BIGINT NOT NULL,
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
        updated_by VARCHAR(100),
        version INTEGER DEFAULT 1
    );

    INSERT INTO products (id, name, original_price, flash_sale_price, updated_by)
    VALUES 
        ('prod_1', 'Sepatu Lari X', 500000, 150000, 'system'),
        ('prod_2', 'Tas Ransel Y', 300000, 99000, 'system')
    ON CONFLICT (id) DO NOTHING;
EOSQL

echo "Inisialisasi tabel untuk db_inventory..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "db_inventory" <<-EOSQL
    CREATE TABLE IF NOT EXISTS inventories (
        product_id VARCHAR(50) PRIMARY KEY,
        stock BIGINT NOT NULL,
        updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
        updated_by VARCHAR(100),
        version INTEGER DEFAULT 1
    );

    CREATE TABLE IF NOT EXISTS outbox_messages (
        id SERIAL PRIMARY KEY,
        aggregate_id VARCHAR(255) NOT NULL,
        aggregate_type VARCHAR(255) NOT NULL,
        event_type VARCHAR(255) NOT NULL,
        payload JSONB NOT NULL,
        trace_payload VARCHAR(512),
        status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );

    INSERT INTO inventories (product_id, stock, updated_by)
    VALUES 
        ('prod_1', 100, 'system'),
        ('prod_2', 50, 'system')
    ON CONFLICT (product_id) DO NOTHING;
EOSQL

echo "Inisialisasi tabel untuk db_order..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "db_order" <<-EOSQL
    CREATE TABLE IF NOT EXISTS orders (
        id VARCHAR(50) PRIMARY KEY,
        user_id VARCHAR(50) NOT NULL,
        product_id VARCHAR(50) NOT NULL,
        quantity INTEGER NOT NULL,
        total_amount BIGINT NOT NULL,
        status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS outbox_messages (
        id SERIAL PRIMARY KEY,
        aggregate_id VARCHAR(255) NOT NULL,
        aggregate_type VARCHAR(255) NOT NULL,
        event_type VARCHAR(255) NOT NULL,
        payload JSONB NOT NULL,
        trace_payload VARCHAR(512),
        status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS processed_events (
        event_id VARCHAR(255) PRIMARY KEY,
        processed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );
EOSQL

echo "Inisialisasi tabel untuk db_payment..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "db_payment" <<-EOSQL
    CREATE TABLE IF NOT EXISTS payments (
        id VARCHAR(50) PRIMARY KEY,
        order_id VARCHAR(50) NOT NULL,
        amount BIGINT NOT NULL,
        status VARCHAR(50) NOT NULL DEFAULT 'SUCCESS',
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS outbox_messages (
        id SERIAL PRIMARY KEY,
        aggregate_id VARCHAR(255) NOT NULL,
        aggregate_type VARCHAR(255) NOT NULL,
        event_type VARCHAR(255) NOT NULL,
        payload JSONB NOT NULL,
        trace_payload VARCHAR(512),
        status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS processed_events (
        event_id VARCHAR(255) PRIMARY KEY,
        processed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );
EOSQL

echo "Inisialisasi tabel untuk db_auth..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "db_auth" <<-EOSQL
    CREATE TABLE IF NOT EXISTS users (
        id VARCHAR(50) PRIMARY KEY,
        username VARCHAR(100) UNIQUE NOT NULL,
        password_hash VARCHAR(255) NOT NULL,
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );
EOSQL

echo "Inisialisasi database selesai."
