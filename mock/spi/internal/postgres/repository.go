// Package postgres implements spi.Store on PostgreSQL.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/spi/internal/spi"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ spi.Store = (*Repository)(nil)

func (r *Repository) AddInbound(ctx context.Context, msgs []spi.Message) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, m := range msgs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO messages (resource_id, direction, ispb, body, created_at) VALUES ($1, 'in', $2, $3, $4)`,
			m.ResourceID, m.ISPB, m.Body, m.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) AddOutbound(ctx context.Context, m spi.Message) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO messages (resource_id, direction, ispb, body, created_at) VALUES ($1, 'out', $2, $3, $4)`,
		m.ResourceID, m.ISPB, m.Body, m.CreatedAt)
	return err
}

func (r *Repository) ReadAfter(ctx context.Context, ispb string, after int64, limit int) ([]spi.Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT seq, resource_id, body, created_at FROM messages
		WHERE direction = 'out' AND ispb = $1 AND seq > $2
		ORDER BY seq LIMIT $3`, ispb, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []spi.Message
	for rows.Next() {
		m := spi.Message{ISPB: ispb}
		if err := rows.Scan(&m.Seq, &m.ResourceID, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (r *Repository) Acked(ctx context.Context, ispb string) (int64, error) {
	var acked int64
	err := r.pool.QueryRow(ctx, `SELECT acked FROM cursors WHERE ispb = $1`, ispb).Scan(&acked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return acked, err
}

func (r *Repository) Ack(ctx context.Context, ispb string, seq int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO cursors (ispb, acked) VALUES ($1, $2)
		ON CONFLICT (ispb) DO UPDATE SET acked = GREATEST(cursors.acked, EXCLUDED.acked)`, ispb, seq)
	return err
}
