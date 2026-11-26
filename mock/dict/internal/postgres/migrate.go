package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/dict/internal/dict"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies, in order, the migrations that have not been applied yet. Each one
// runs in its own transaction; an advisory lock prevents races between instances.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	const lockID = 727274 // arbitrary, exclusive to this service
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockID) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, f := range files {
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, f).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrationsFS.ReadFile(f)
		if err != nil {
			return err
		}
		if err := applyMigration(ctx, conn, f, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", f, err)
		}
	}
	if err := backfillCIDs(ctx, conn); err != nil {
		return fmt.Errorf("backfill CIDs: %w", err)
	}
	return nil
}

// backfillCIDs gives a CID, and its ADDED event, to the entries created before the reconciliation existed.
func backfillCIDs(ctx context.Context, conn *pgxpool.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT `+selectColumns+` FROM entries WHERE cid IS NULL ORDER BY creation_date, key`)
	if err != nil {
		return err
	}
	var todo []dict.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range todo {
		cid, err := dict.CID(e, e.RequestID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE entries SET cid = $2 WHERE key = $1`, e.Key, cid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cid_events (participant, key_type, type, cid, at) VALUES ($1, $2, 'ADDED', $3, $4)`,
			e.Account.Participant, e.KeyType, cid, e.CreationDate); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, name, sql string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
