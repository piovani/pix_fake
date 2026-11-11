// Package pgtest prepara um Postgres isolado para testes de integração.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/dict/internal/postgres"
)

// NewPool devolve um pool ligado a um schema exclusivo do teste, já migrado e
// removido ao final. Assim pacotes de teste rodando em paralelo não dividem
// tabelas. Sem DICT_TEST_DATABASE_URL o teste é pulado.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DICT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DICT_TEST_DATABASE_URL not set; run `make test-integration`")
	}
	ctx := context.Background()

	var b [6]byte
	_, _ = rand.Read(b[:])
	schema := "test_" + hex.EncodeToString(b[:])

	admin, err := pgxpool.New(ctx, url)
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

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
