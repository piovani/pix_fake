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

func (r *Repository) StartStream(ctx context.Context, ispb, token string) error {
	// Streams that were never closed are dropped after an hour.
	if _, err := r.pool.Exec(ctx, `DELETE FROM streams WHERE created_at < now() - interval '1 hour'`); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO streams (token, ispb, cursor)
		VALUES ($1, $2, COALESCE((SELECT acked FROM cursors WHERE ispb = $2), 0))`, token, ispb)
	return err
}

func (r *Repository) Read(ctx context.Context, ispb, token, newToken string, limit int) ([]spi.Message, string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cursor int64
	err = tx.QueryRow(ctx, `SELECT cursor FROM streams WHERE token = $1 AND ispb = $2`, token, ispb).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", spi.ErrStreamGone
	}
	if err != nil {
		return nil, "", err
	}

	rows, err := tx.Query(ctx, `
		SELECT seq, resource_id, body, created_at FROM messages
		WHERE direction = 'out' AND ispb = $1 AND seq > $2
		ORDER BY seq LIMIT $3`, ispb, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	var msgs []spi.Message
	for rows.Next() {
		m := spi.Message{ISPB: ispb}
		if err := rows.Scan(&m.Seq, &m.ResourceID, &m.Body, &m.CreatedAt); err != nil {
			rows.Close()
			return nil, "", err
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(msgs) == 0 {
		return nil, token, nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO streams (token, ispb, cursor) VALUES ($1, $2, $3)`,
		newToken, ispb, msgs[len(msgs)-1].Seq); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return msgs, newToken, nil
}

func (r *Repository) Ack(ctx context.Context, ispb, token string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cursor int64
	err = tx.QueryRow(ctx, `DELETE FROM streams WHERE token = $1 AND ispb = $2 RETURNING cursor`, token, ispb).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return spi.ErrStreamGone
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cursors (ispb, acked) VALUES ($1, $2)
		ON CONFLICT (ispb) DO UPDATE SET acked = GREATEST(cursors.acked, EXCLUDED.acked)`, ispb, cursor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
