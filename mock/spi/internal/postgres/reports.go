package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"pixfake/mock/spi/internal/pix"
)

var _ pix.ReportStore = (*Repository)(nil)

func (r *Repository) AddReported(ctx context.Context, t pix.ReportedTransaction) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO reported_transactions (id, end_to_end_id, scenario, initiation, amount_cents, settled_at,
			payer_ispb, payer_tax_id, payer_branch, payer_account, payer_account_type,
			payee_ispb, payee_tax_id, payee_branch, payee_account, payee_account_type, payee_key,
			reported_by, reported_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.EndToEndID, t.Scenario, t.Initiation, t.AmountCents, t.SettledAt,
		t.PayerISPB, t.PayerTaxID, t.PayerBranch, t.PayerAccount, t.PayerAccountType,
		t.PayeeISPB, t.PayeeTaxID, t.PayeeBranch, t.PayeeAccount, t.PayeeAccountType, t.PayeeKey,
		t.ReportedBy, t.ReportedAt)
	return tag.RowsAffected() == 1, err
}

func (r *Repository) GetReported(ctx context.Context, id string) (pix.ReportedTransaction, error) {
	var t pix.ReportedTransaction
	err := r.pool.QueryRow(ctx, `
		SELECT id, end_to_end_id, scenario, initiation, amount_cents, settled_at,
			payer_ispb, payer_tax_id, payer_branch, payer_account, payer_account_type,
			payee_ispb, payee_tax_id, payee_branch, payee_account, payee_account_type, payee_key,
			reported_by, reported_at
		FROM reported_transactions WHERE id = $1`, id).
		Scan(&t.ID, &t.EndToEndID, &t.Scenario, &t.Initiation, &t.AmountCents, &t.SettledAt,
			&t.PayerISPB, &t.PayerTaxID, &t.PayerBranch, &t.PayerAccount, &t.PayerAccountType,
			&t.PayeeISPB, &t.PayeeTaxID, &t.PayeeBranch, &t.PayeeAccount, &t.PayeeAccountType, &t.PayeeKey,
			&t.ReportedBy, &t.ReportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return pix.ReportedTransaction{}, pix.ErrReportedNotFound
	}
	return t, err
}
