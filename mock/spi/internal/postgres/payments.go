package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"pixfake/mock/spi/internal/pix"
)

var _ pix.PaymentStore = (*Repository)(nil)

const paymentColumns = `id, kind, end_to_end_id, return_reason, status, reason_code, reason_detail, amount_cents, purpose, initiation, info, priority,
	payer_ispb, payer_name, payer_tax_id, payer_branch, payer_account, payer_account_type,
	payee_ispb, payee_tax_id, payee_branch, payee_account, payee_account_type, payee_key,
	accepted_at, created_at, settled_at`

func scanPayment(row pgx.Row) (pix.Payment, error) {
	var (
		p         pix.Payment
		settledAt *time.Time
	)
	err := row.Scan(&p.ID, &p.Kind, &p.EndToEndID, &p.ReturnReason, &p.Status, &p.ReasonCode, &p.ReasonDetail, &p.AmountCents, &p.Purpose, &p.Initiation, &p.Info, &p.Priority,
		&p.PayerISPB, &p.PayerName, &p.PayerTaxID, &p.PayerBranch, &p.PayerAccount, &p.PayerAccountType,
		&p.PayeeISPB, &p.PayeeTaxID, &p.PayeeBranch, &p.PayeeAccount, &p.PayeeAccountType, &p.PayeeKey,
		&p.AcceptedAt, &p.CreatedAt, &settledAt)
	if settledAt != nil {
		p.SettledAt = *settledAt
	}
	return p, err
}

func (r *Repository) CreatePayment(ctx context.Context, p pix.Payment) (pix.Payment, bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO payments (id, kind, end_to_end_id, return_reason, status, reason_code, reason_detail, amount_cents, purpose, initiation, info, priority,
			payer_ispb, payer_name, payer_tax_id, payer_branch, payer_account, payer_account_type,
			payee_ispb, payee_tax_id, payee_branch, payee_account, payee_account_type, payee_key,
			accepted_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
		ON CONFLICT (id) DO NOTHING`,
		p.ID, p.Kind, p.EndToEndID, p.ReturnReason, p.Status, p.ReasonCode, p.ReasonDetail, p.AmountCents, p.Purpose, p.Initiation, p.Info, p.Priority,
		p.PayerISPB, p.PayerName, p.PayerTaxID, p.PayerBranch, p.PayerAccount, p.PayerAccountType,
		p.PayeeISPB, p.PayeeTaxID, p.PayeeBranch, p.PayeeAccount, p.PayeeAccountType, p.PayeeKey,
		p.AcceptedAt, p.CreatedAt)
	if err != nil {
		return pix.Payment{}, false, err
	}
	if tag.RowsAffected() == 1 {
		return p, true, nil
	}
	existing, err := r.GetPayment(ctx, p.ID)
	return existing, false, err
}

func (r *Repository) GetPayment(ctx context.Context, id string) (pix.Payment, error) {
	p, err := scanPayment(r.pool.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return pix.Payment{}, pix.ErrPaymentNotFound
	}
	return p, err
}

func (r *Repository) ReturnsOf(ctx context.Context, endToEndID string) ([]pix.Payment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+paymentColumns+` FROM payments WHERE kind = 'RETURN' AND end_to_end_id = $1 ORDER BY created_at`, endToEndID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pix.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) PendingBefore(ctx context.Context, t time.Time) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM payments WHERE status = 'PENDING' AND created_at <= $1`, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repository) FinalizePayment(ctx context.Context, id, status, reasonCode, reasonDetail string, at time.Time) (pix.Payment, bool, error) {
	var settledAt *time.Time
	if status == pix.StatusSettled {
		settledAt = &at
	}
	p, err := scanPayment(r.pool.QueryRow(ctx, `
		UPDATE payments SET status = $2, reason_code = $3, reason_detail = $4, settled_at = $5
		WHERE id = $1 AND status = 'PENDING'
		RETURNING `+paymentColumns, id, status, reasonCode, reasonDetail, settledAt))
	if err == nil {
		return p, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pix.Payment{}, false, err
	}
	// Not updated: it does not exist or is no longer pending.
	current, err := r.GetPayment(ctx, id)
	return current, false, err
}
