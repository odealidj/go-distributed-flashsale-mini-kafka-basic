//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	outboundPostgres "go-flashsale-mini-kafka-basic/inventory-service/internal/adapter/outbound/postgres"
)

// setupTestDB adalah helper yang membuat Postgres container dan tabel outbox.
// Dipanggil oleh setiap test case agar isolasinya terjaga.
func setupTestDB(t *testing.T) (*sqlx.DB, func()) {
	t.Helper()
	ctx := context.Background()

	dbName := "db_inventory"
	dbUser := "postgres"
	dbPassword := "testcontainers"

	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15-alpine"),
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sqlx.Connect("postgres", connStr)
	require.NoError(t, err)

	createTableQuery := `
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
	`
	_, err = db.Exec(createTableQuery)
	require.NoError(t, err)

	teardown := func() {
		db.Close()
		_ = pgContainer.Terminate(ctx)
	}
	return db, teardown
}

func TestOutboxRepo_InsertOutbox(t *testing.T) {
	db, teardown := setupTestDB(t)
	defer teardown()

	ctx := context.Background()
	repo := outboundPostgres.NewOutboxRepo(db)

	aggregateID := "evt-insert-001"
	aggregateType := "Inventory"
	eventType := "StockReservedEvent"
	payload := []byte(`{"product_id": "prod_1", "quantity": 1, "status": "RESERVED"}`)

	// Act
	err := repo.InsertOutbox(ctx, aggregateID, aggregateType, eventType, payload)
	assert.NoError(t, err)

	// Assert: record ada di database
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM outbox_messages WHERE aggregate_id = $1", aggregateID)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	type outboxMsg struct {
		AggregateID   string `db:"aggregate_id"`
		AggregateType string `db:"aggregate_type"`
		EventType     string `db:"event_type"`
		Payload       string `db:"payload"`
		Status        string `db:"status"`
	}
	var msg outboxMsg
	err = db.Get(&msg,
		"SELECT aggregate_id, aggregate_type, event_type, payload, status FROM outbox_messages WHERE aggregate_id = $1",
		aggregateID,
	)
	assert.NoError(t, err)
	assert.Equal(t, aggregateID, msg.AggregateID)
	assert.Equal(t, aggregateType, msg.AggregateType)
	assert.Equal(t, eventType, msg.EventType)
	assert.JSONEq(t, string(payload), msg.Payload)
	assert.Equal(t, "PENDING", msg.Status)
}

func TestOutboxRepo_IsOutboxExist(t *testing.T) {
	db, teardown := setupTestDB(t)
	defer teardown()

	ctx := context.Background()
	repo := outboundPostgres.NewOutboxRepo(db)

	t.Run("IsOutboxExist - returns true when record exists", func(t *testing.T) {
		aggregateID := "evt-exist-001"
		payload := []byte(`{"product_id": "prod_1", "quantity": 1}`)

		// Masukkan dulu satu record
		err := repo.InsertOutbox(ctx, aggregateID, "Inventory", "StockReservedEvent", payload)
		require.NoError(t, err)

		// Cek keberadaannya
		exists, err := repo.IsOutboxExist(ctx, aggregateID)
		assert.NoError(t, err)
		assert.True(t, exists, "Seharusnya mengembalikan true karena record sudah ada")
	})

	t.Run("IsOutboxExist - returns false when record does not exist", func(t *testing.T) {
		// aggregateID yang tidak pernah di-insert
		aggregateID := "evt-not-exist-999"

		exists, err := repo.IsOutboxExist(ctx, aggregateID)
		assert.NoError(t, err)
		assert.False(t, exists, "Seharusnya mengembalikan false karena record tidak ada")
	})

	t.Run("IsOutboxExist - idempotent for multiple inserts", func(t *testing.T) {
		aggregateID := "evt-multi-001"
		payload := []byte(`{"product_id": "prod_2", "quantity": 2}`)

		// Insert dua kali (dalam skenario nyata ini tidak terjadi karena primary key,
		// tapi kita memastikan IsOutboxExist tidak error jika ada lebih dari 1 record)
		_ = repo.InsertOutbox(ctx, aggregateID, "Inventory", "StockReservedEvent", payload)
		_ = repo.InsertOutbox(ctx, aggregateID, "Inventory", "StockReservedEvent", payload)

		exists, err := repo.IsOutboxExist(ctx, aggregateID)
		assert.NoError(t, err)
		assert.True(t, exists)
	})
}

// TestOutboxRepo_Reconciliation_Scenario mensimulasikan skenario stock leak end-to-end:
// 1. Event ada di Redis (idempotency key), TIDAK ada di Postgres (outbox gagal).
// 2. ReconciliationJob mendeteksi dan memverifikasi lewat IsOutboxExist.
func TestOutboxRepo_Reconciliation_Scenario(t *testing.T) {
	db, teardown := setupTestDB(t)
	defer teardown()

	ctx := context.Background()
	repo := outboundPostgres.NewOutboxRepo(db)

	// Skenario: eventID ada di Redis tapi TIDAK ada di Postgres → stock leak
	leakedEventID := "evt-leaked-reconcile-001"

	exists, err := repo.IsOutboxExist(ctx, leakedEventID)
	assert.NoError(t, err)
	assert.False(t, exists, "Event bocor tidak boleh ada di Postgres — ReconciliationJob harus refund stok")

	// Skenario: eventID yang SUDAH berhasil masuk ke Postgres → bukan leak
	successEventID := "evt-success-reconcile-001"
	payload := []byte(`{"product_id": "prod_3", "quantity": 1}`)
	err = repo.InsertOutbox(ctx, successEventID, "Inventory", "StockReservedEvent", payload)
	require.NoError(t, err)

	exists, err = repo.IsOutboxExist(ctx, successEventID)
	assert.NoError(t, err)
	assert.True(t, exists, "Event yang berhasil masuk Postgres bukan leak, tidak perlu di-refund")
}
