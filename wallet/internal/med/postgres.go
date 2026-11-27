package med

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the Store in the wallet's database.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const reportColumns = `id, transaction_id, COALESCE(payment_id::text, ''), COALESCE(account_id::text, ''), status, situation, details,
	amount_cents, COALESCE(block_id::text, ''), blocked_cents, block_status, result, created_at, updated_at`

func scanReport(row pgx.Row) (Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.TransactionID, &r.PaymentID, &r.AccountID, &r.Status, &r.Situation, &r.Details,
		&r.AmountCents, &r.BlockID, &r.BlockedCents, &r.BlockStatus, &r.Result, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (p *Postgres) Report(ctx context.Context, id string) (Report, bool, error) {
	r, err := scanReport(p.pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM med_reports WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, false, nil
	}
	return r, err == nil, err
}

func (p *Postgres) SaveReport(ctx context.Context, r Report) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO med_reports (id, transaction_id, payment_id, account_id, status, situation, details, amount_cents,
			block_id, blocked_cents, block_status, result, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET payment_id = EXCLUDED.payment_id, account_id = EXCLUDED.account_id,
			status = EXCLUDED.status, block_id = EXCLUDED.block_id, blocked_cents = EXCLUDED.blocked_cents,
			block_status = EXCLUDED.block_status, result = EXCLUDED.result, updated_at = EXCLUDED.updated_at`,
		r.ID, r.TransactionID, nullable(r.PaymentID), nullable(r.AccountID), r.Status, r.Situation, r.Details, r.AmountCents,
		nullable(r.BlockID), r.BlockedCents, r.BlockStatus, r.Result, r.CreatedAt, r.UpdatedAt)
	return err
}

func (p *Postgres) BlockedFor(ctx context.Context, transactionID string) (Report, bool, error) {
	r, err := scanReport(p.pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM med_reports
		WHERE transaction_id = $1 AND result = 'AGREED' AND block_status = 'HELD' ORDER BY created_at LIMIT 1`, transactionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, false, nil
	}
	return r, err == nil, err
}

func (p *Postgres) AwaitingAnalysis(ctx context.Context) ([]Report, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+reportColumns+` FROM med_reports WHERE status = 'ACKNOWLEDGED' AND result = '' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const refundColumns = `id, transaction_id, COALESCE(payment_id::text, ''), reason, amount_cents, status, request_id::text,
	return_cents, COALESCE(return_payment_id::text, ''), result, rejection_reason, created_at, updated_at`

func scanRefund(row pgx.Row) (Refund, error) {
	var f Refund
	err := row.Scan(&f.ID, &f.TransactionID, &f.PaymentID, &f.Reason, &f.AmountCents, &f.Status, &f.RequestID,
		&f.ReturnCents, &f.ReturnPaymentID, &f.Result, &f.RejectionReason, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func (p *Postgres) Refund(ctx context.Context, id string) (Refund, bool, error) {
	f, err := scanRefund(p.pool.QueryRow(ctx, `SELECT `+refundColumns+` FROM med_refunds WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Refund{}, false, nil
	}
	return f, err == nil, err
}

func (p *Postgres) InsertRefund(ctx context.Context, f Refund) (Refund, bool, error) {
	stored, err := scanRefund(p.pool.QueryRow(ctx, `
		INSERT INTO med_refunds (id, transaction_id, reason, amount_cents, status, request_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+refundColumns, f.ID, f.TransactionID, f.Reason, f.AmountCents, f.Status, f.RequestID, f.CreatedAt, f.UpdatedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, _, err := p.Refund(ctx, f.ID)
		return existing, false, err
	}
	return stored, err == nil, err
}

func (p *Postgres) SaveRefund(ctx context.Context, f Refund) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE med_refunds SET payment_id = $2, status = $3, return_cents = $4, return_payment_id = $5, result = $6,
			rejection_reason = $7, updated_at = $8
		WHERE id = $1`,
		f.ID, nullable(f.PaymentID), f.Status, f.ReturnCents, nullable(f.ReturnPaymentID), f.Result, f.RejectionReason, f.UpdatedAt)
	return err
}

func (p *Postgres) Returning(ctx context.Context, limit int) ([]Refund, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+refundColumns+` FROM med_refunds WHERE status = 'RETURNING' ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Refund
	for rows.Next() {
		f, err := scanRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (p *Postgres) Watermark(ctx context.Context, name string) (time.Time, error) {
	var at time.Time
	err := p.pool.QueryRow(ctx, `SELECT at FROM med_watermarks WHERE name = $1`, name).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return at, err
}

func (p *Postgres) SetWatermark(ctx context.Context, name string, at time.Time) error {
	if at.IsZero() {
		return nil
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO med_watermarks (name, at) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET at = GREATEST(med_watermarks.at, EXCLUDED.at)`, name, at)
	return err
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
