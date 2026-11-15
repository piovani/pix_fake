package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"pixfake/mock/spi/internal/pix"
)

var _ pix.AccountStore = (*Repository)(nil)

func (r *Repository) CreateAccount(ctx context.Context, a pix.Account) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO accounts (ispb, number, branch, type, holder_tax_id, holder_name, status, scenario)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (ispb, number) DO NOTHING`,
		a.ISPB, a.Number, a.Branch, a.Type, a.HolderTaxID, a.HolderName, a.Status, a.Scenario)
	return tag.RowsAffected() == 1, err
}

func (r *Repository) GetAccount(ctx context.Context, ispb, number string) (pix.Account, error) {
	var a pix.Account
	err := r.pool.QueryRow(ctx, `
		SELECT ispb, number, branch, type, holder_tax_id, holder_name, status, scenario
		FROM accounts WHERE ispb = $1 AND number = $2`, ispb, number).
		Scan(&a.ISPB, &a.Number, &a.Branch, &a.Type, &a.HolderTaxID, &a.HolderName, &a.Status, &a.Scenario)
	if errors.Is(err, pgx.ErrNoRows) {
		return pix.Account{}, pix.ErrAccountNotFound
	}
	return a, err
}
