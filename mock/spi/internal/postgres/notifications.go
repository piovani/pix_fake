package postgres

import (
	"context"
	"time"

	"pixfake/mock/spi/internal/pix"
)

var _ pix.NotificationStore = (*Repository)(nil)

func (r *Repository) PendingNotifications(ctx context.Context, since time.Time, limit int) ([]pix.Payment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+paymentColumns+` FROM payments
		WHERE kind = 'PAYMENT' AND status = 'SETTLED' AND dict_notified_at IS NULL AND settled_at >= $1
		ORDER BY settled_at, id LIMIT $2`, since, limit)
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

func (r *Repository) MarkNotified(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE payments SET dict_notified_at = $2 WHERE id = $1 AND dict_notified_at IS NULL`, id, at)
	return err
}
