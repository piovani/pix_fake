// Package dbtest prepares an isolated PostgreSQL for integration tests.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/wallet/internal/platform/db"
)

// NewPool returns a pool bound to a schema exclusive to the test, already migrated and dropped at the end, so test
// packages running in parallel do not share tables. Without WALLET_TEST_DATABASE_URL the test is skipped.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("WALLET_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("WALLET_TEST_DATABASE_URL not set; run `make test-integration` in wallet/")
	}
	ctx := context.Background()

	var b [6]byte
	_, _ = rand.Read(b[:])
	schema := "test_" + hex.EncodeToString(b[:])

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(ctx), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	scoped := u.String()

	if err := db.Migrate(scoped); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
