package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"pixfake/mock/spi/internal/pix"
)

var _ pix.PaymentStore = (*Repository)(nil)

const paymentColumns = `end_to_end_id, status, reason_code, reason_detail, amount_cents, purpose, initiation, info, priority,
	payer_ispb, payer_name, payer_tax_id, payer_branch, payer_account, payer_account_type,
	payee_ispb, payee_tax_id, payee_branch, payee_account, payee_account_type, payee_key,
	accepted_at, created_at, settled_at`

func scanPayment(row pgx.Row) (pix.Payment, error) {
	var (
		p         pix.Payment
		settledAt *time.Time
	)
	err := row.Scan(&p.EndToEndID, &p.Status, &p.ReasonCode, &p.ReasonDetail, &p.AmountCents, &p.Purpose, &p.Initiation, &p.Info, &p.Priority,
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
		INSERT INTO payments (end_to_end_id, status, reason_code, reason_detail, amount_cents, purpose, initiation, info, priority,
			payer_ispb, payer_name, payer_tax_id, payer_branch, payer_account, payer_account_type,
			payee_ispb, payee_tax_id, payee_branch, payee_account, payee_account_type, payee_key,
			accepted_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (end_to_end_id) DO NOTHING`,
		p.EndToEndID, p.Status, p.ReasonCode, p.ReasonDetail, p.AmountCents, p.Purpose, p.Initiation, p.Info, p.Priority,
		p.PayerISPB, p.PayerName, p.PayerTaxID, p.PayerBranch, p.PayerAccount, p.PayerAccountType,
		p.PayeeISPB, p.PayeeTaxID, p.PayeeBranch, p.PayeeAccount, p.PayeeAccountType, p.PayeeKey,
		p.AcceptedAt, p.CreatedAt)
	if err != nil {
		return pix.Payment{}, false, err
	}
	if tag.RowsAffected() == 1 {
		return p, true, nil
	}
	existing, err := r.GetPayment(ctx, p.EndToEndID)
	return existing, false, err
}

func (r *Repository) GetPayment(ctx context.Context, endToEndID string) (pix.Payment, error) {
	p, err := scanPayment(r.pool.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE end_to_end_id = $1`, endToEndID))
	if errors.Is(err, pgx.ErrNoRows) {
		return pix.Payment{}, pix.ErrPaymentNotFound
	}
	return p, err
}

func (r *Repository) FinalizePayment(ctx context.Context, endToEndID, status, reasonCode, reasonDetail string, at time.Time) (pix.Payment, bool, error) {
	var settledAt *time.Time
	if status == pix.StatusSettled {
		settledAt = &at
	}
	p, err := scanPayment(r.pool.QueryRow(ctx, `
		UPDATE payments SET status = $2, reason_code = $3, reason_detail = $4, settled_at = $5
		WHERE end_to_end_id = $1 AND status = 'PENDING'
		RETURNING `+paymentColumns, endToEndID, status, reasonCode, reasonDetail, settledAt))
	if err == nil {
		return p, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pix.Payment{}, false, err
	}
	// Not updated: it does not exist or is no longer pending.
	current, err := r.GetPayment(ctx, endToEndID)
	return current, false, err
}
