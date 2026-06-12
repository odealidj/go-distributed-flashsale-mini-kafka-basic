#!/bin/bash

COMMAND=$1

case $COMMAND in
    "seed")
        echo "Menambahkan contoh data ke Database dan Redis..."
        
        # Insert ke Postgres
        docker exec -i flashsale-postgres psql -U root -d db_inventory -c "
        INSERT INTO inventories (product_id, stock, updated_at, updated_by, version)
        VALUES 
            ('product-dummy-01', 500, NOW(), 'system', 1),
            ('product-dummy-02', 1000, NOW(), 'system', 1)
        ON CONFLICT (product_id) DO UPDATE SET stock = EXCLUDED.stock;
        "
        
        # Set ke Redis
        docker exec -i flashsale-redis redis-cli SET stock:product-dummy-01 500
        docker exec -i flashsale-redis redis-cli SET stock:product-dummy-02 1000
        
        echo "✅ Contoh data berhasil ditambahkan!"
        echo "   - Product ID: product-dummy-01 (Stok: 500)"
        echo "   - Product ID: product-dummy-02 (Stok: 1000)"
        ;;
        
    "cleanup")
        echo "Menghapus contoh data dari Database dan Redis..."
        
        # Hapus dari Postgres
        docker exec -i flashsale-postgres psql -U root -d db_inventory -c "
        DELETE FROM inventories WHERE product_id IN ('product-dummy-01', 'product-dummy-02');
        "
        
        # Hapus dari Redis
        docker exec -i flashsale-redis redis-cli DEL stock:product-dummy-01
        docker exec -i flashsale-redis redis-cli DEL stock:product-dummy-02
        
        echo "🗑️ Contoh data berhasil dihapus!"
        ;;
        
    *)
        echo "Penggunaan: ./scripts/manage_test_data.sh [seed|cleanup]"
        echo "  seed    : Menambahkan dummy produk dan stok ke DB & Redis"
        echo "  cleanup : Menghapus dummy produk dan stok dari DB & Redis"
        ;;
esac
