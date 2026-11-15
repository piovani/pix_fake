package postgres

import (
	"context"

	"pixfake/mock/spi/internal/pix"
)

var _ pix.Inspector = (*Repository)(nil)

func (r *Repository) ListPayments(ctx context.Context, f pix.PaymentFilter) ([]pix.Payment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+paymentColumns+` FROM payments
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR kind = $2) AND ($3 = '' OR payer_ispb = $3 OR payee_ispb = $3)
		ORDER BY created_at DESC, id LIMIT $4`, f.Status, f.Kind, f.Participant, pix.ClampLimit(f.Limit))
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

func (r *Repository) ListMessages(ctx context.Context, f pix.MessageFilter) ([]pix.StoredMessage, error) {
	rows, err := r.pool.Query(ctx, `SELECT seq, resource_id, ispb, direction, created_at, convert_from(body, 'UTF8') FROM messages
		WHERE ($1 = '' OR ispb = $1) AND ($2 = '' OR direction = $2)
		ORDER BY seq DESC LIMIT $3`, f.Participant, f.Direction, pix.ClampLimit(f.Limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pix.StoredMessage
	for rows.Next() {
		var m pix.StoredMessage
		if err := rows.Scan(&m.Seq, &m.ResourceID, &m.Participant, &m.Direction, &m.CreatedAt, &m.Body); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repository) ListAccounts(ctx context.Context) ([]pix.Account, error) {
	rows, err := r.pool.Query(ctx, `SELECT ispb, number, branch, type, holder_tax_id, holder_name, status, scenario
		FROM accounts ORDER BY ispb, number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pix.Account
	for rows.Next() {
		var a pix.Account
		if err := rows.Scan(&a.ISPB, &a.Number, &a.Branch, &a.Type, &a.HolderTaxID, &a.HolderName, &a.Status, &a.Scenario); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Reset keeps the accounts, and the sequence of messages (no RESTART IDENTITY), so a stream token a client still holds
// stays meaningful.
func (r *Repository) Reset(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `TRUNCATE messages, cursors, payments, reported_transactions`)
	return err
}
