package payment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/platform/db"
)

// Postgres is the Store in the wallet's database.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

type payeeJSON struct {
	Name          string `json:"name"`
	TaxID         string `json:"tax_id"`
	Participant   string `json:"participant"`
	Branch        string `json:"branch"`
	AccountNumber string `json:"account_number"`
	AccountType   string `json:"account_type"`
	Key           string `json:"key"`
}

func marshalPayee(p Payee) ([]byte, error) {
	return json.Marshal(payeeJSON{p.Name, p.TaxID, p.Participant, p.Branch, p.AccountNumber, string(p.AccountType), p.Key})
}

func unmarshalPayee(raw []byte) (Payee, error) {
	var j payeeJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return Payee{}, err
	}
	return Payee{j.Name, j.TaxID, j.Participant, j.Branch, j.AccountNumber, pix.AccountType(j.AccountType), j.Key}, nil
}

func (p *Postgres) SaveResolution(ctx context.Context, r Resolution) (Resolution, error) {
	payee, err := marshalPayee(r.Payee)
	if err != nil {
		return Resolution{}, err
	}
	err = p.pool.QueryRow(ctx, `
		INSERT INTO resolutions (account_id, key, end_to_end_id, payee, expires_at, book_transfer, payee_account_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`, r.AccountID, r.Key, r.EndToEndID, payee, r.ExpiresAt, r.BookTransfer, nullable(r.PayeeAccountID)).Scan(&r.ID)
	return r, err
}

func (p *Postgres) Resolution(ctx context.Context, id string) (Resolution, bool, error) {
	var r Resolution
	var payee []byte
	var payeeAccount *string
	err := p.pool.QueryRow(ctx, `
		SELECT id, account_id, key, end_to_end_id, payee, expires_at, book_transfer, payee_account_id
		FROM resolutions WHERE id = $1`, id).
		Scan(&r.ID, &r.AccountID, &r.Key, &r.EndToEndID, &payee, &r.ExpiresAt, &r.BookTransfer, &payeeAccount)
	if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") { // not found, or not a UUID
		return Resolution{}, false, nil
	}
	if err != nil {
		return Resolution{}, false, err
	}
	r.PayeeAccountID = deref(payeeAccount)
	r.Payee, err = unmarshalPayee(payee)
	return r, err == nil, err
}

const columns = `id, account_id, direction, type, status, amount_cents, end_to_end_id, resolution_id, reservation_id,
	counterparty, message, reject_code, reject_reason, resource_id, submitted_at, created_at, finished_at,
	report_status, report_sent_at, report_reason, original_payment_id, request_id, return_reason,
	(SELECT COALESCE(SUM(r.amount_cents), 0)::bigint FROM payments r
	 WHERE r.original_payment_id = payments.id AND r.status <> 'REJECTED')`

func scan(row pgx.Row) (Payment, error) {
	var p Payment
	var direction, typ, status string
	var resolution, reservation, resource, original, request *string
	var counterparty []byte
	var submitted, finished, reportSent *time.Time
	err := row.Scan(&p.ID, &p.AccountID, &direction, &typ, &status, &p.AmountCents, &p.EndToEndID, &resolution, &reservation,
		&counterparty, &p.Message, &p.RejectCode, &p.RejectReason, &resource, &submitted, &p.CreatedAt, &finished,
		&p.ReportStatus, &reportSent, &p.ReportReason, &original, &request, &p.ReturnReason, &p.ReturnedCents)
	if err != nil {
		return Payment{}, err
	}
	p.Direction, p.Type, p.Status = Direction(direction), Type(typ), Status(status)
	p.ResolutionID, p.ReservationID, p.ResourceID = deref(resolution), deref(reservation), deref(resource)
	p.OriginalPaymentID, p.RequestID = deref(original), deref(request)
	if submitted != nil {
		p.SubmittedAt = *submitted
	}
	if finished != nil {
		p.FinishedAt = *finished
	}
	if reportSent != nil {
		p.ReportSentAt = *reportSent
	}
	p.Counterparty, err = unmarshalPayee(counterparty)
	return p, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (p *Postgres) Insert(ctx context.Context, in Payment) (Payment, bool, error) {
	cp, err := marshalPayee(in.Counterparty)
	if err != nil {
		return Payment{}, false, err
	}
	stored, err := scan(p.pool.QueryRow(ctx, `
		INSERT INTO payments (account_id, direction, type, status, amount_cents, end_to_end_id, resolution_id, reservation_id,
			counterparty, message, created_at, finished_at, original_payment_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING
		RETURNING `+columns,
		in.AccountID, string(in.Direction), string(in.Type), string(in.Status), in.AmountCents, in.EndToEndID,
		nullable(in.ResolutionID), nullable(in.ReservationID), cp, in.Message, in.CreatedAt, nullTime(in.FinishedAt),
		nullable(in.OriginalPaymentID)))
	if errors.Is(err, pgx.ErrNoRows) { // the resolution already made a payment, or the EndToEndId already has one in this direction
		existing, found, err := p.existingOf(ctx, in)
		if err == nil && !found {
			err = pgx.ErrNoRows
		}
		return existing, false, err
	}
	return stored, err == nil, err
}

func (p *Postgres) existingOf(ctx context.Context, in Payment) (Payment, bool, error) {
	if in.ResolutionID != "" {
		if existing, found, err := p.ByResolution(ctx, in.ResolutionID); err != nil || found {
			return existing, found, err
		}
	}
	return p.ByEndToEnd(ctx, in.EndToEndID, in.Direction)
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (p *Postgres) Discard(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM payments WHERE id = $1 AND status = 'PENDING'`, id)
	return err
}

func (p *Postgres) one(ctx context.Context, where string, args ...any) (Payment, bool, error) {
	got, err := scan(p.pool.QueryRow(ctx, `SELECT `+columns+` FROM payments WHERE `+where, args...))
	if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") {
		return Payment{}, false, nil
	}
	return got, err == nil, err
}

func (p *Postgres) ByID(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `id = $1`, id)
}
func (p *Postgres) ByEndToEnd(ctx context.Context, e string, d Direction) (Payment, bool, error) {
	return p.one(ctx, `end_to_end_id = $1 AND direction = $2`, e, string(d))
}
func (p *Postgres) ByResolution(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `resolution_id = $1`, id)
}
func (p *Postgres) ByResource(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `resource_id = $1`, id)
}

func (p *Postgres) ByReservation(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `reservation_id = $1`, id)
}

func (p *Postgres) ByRequestID(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `request_id = $1`, id)
}

func (p *Postgres) ByReferences(ctx context.Context, accountID string, refs []string) ([]Payment, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	return p.many(ctx, `
		SELECT `+columns+` FROM payments
		WHERE account_id = $1 AND (end_to_end_id = ANY($2::text[]) OR request_id::text = ANY($2::text[]))`, accountID, refs)
}

func (p *Postgres) InsertReturn(ctx context.Context, in Payment) (Payment, bool, error) {
	cp, err := marshalPayee(in.Counterparty)
	if err != nil {
		return Payment{}, false, err
	}
	var stored Payment
	created := false
	err = db.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		// the lock serializes the returns of one payment, so two of them cannot both fit in what is left
		var original, returned int64
		if err := tx.QueryRow(ctx, `SELECT amount_cents FROM payments WHERE id = $1 FOR UPDATE`, in.OriginalPaymentID).Scan(&original); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPaymentNotFound
			}
			return err
		}
		existing, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM payments WHERE request_id = $1`, in.RequestID))
		if err == nil {
			stored = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_cents), 0)::bigint FROM payments WHERE original_payment_id = $1 AND status <> 'REJECTED'`,
			in.OriginalPaymentID).Scan(&returned); err != nil {
			return err
		}
		if returned+in.AmountCents > original {
			return ErrReturnTooLarge
		}
		stored, err = scan(tx.QueryRow(ctx, `
			INSERT INTO payments (account_id, direction, type, status, amount_cents, end_to_end_id, reservation_id, counterparty,
				original_payment_id, request_id, return_reason, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING `+columns,
			in.AccountID, string(in.Direction), string(in.Type), string(in.Status), in.AmountCents, in.EndToEndID,
			nullable(in.ReservationID), cp, in.OriginalPaymentID, in.RequestID, in.ReturnReason, in.CreatedAt))
		created = err == nil
		return err
	})
	return stored, created, err
}

func (p *Postgres) MarkSubmitted(ctx context.Context, id, resourceID string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE payments SET resource_id = $2, submitted_at = $3 WHERE id = $1`,
		id, nullable(resourceID), at)
	return err
}

func (p *Postgres) Close(ctx context.Context, id string, status Status, code, reason string, at time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE payments SET status = $2, reject_code = $3, reject_reason = $4, finished_at = $5
		WHERE id = $1 AND status = 'PENDING'`, id, string(status), code, reason, at)
	return tag.RowsAffected() == 1, err
}

func (p *Postgres) Unsent(ctx context.Context, before time.Time, limit int) ([]Payment, error) {
	return p.many(ctx, `
		SELECT `+columns+` FROM payments
		WHERE status = 'PENDING' AND submitted_at IS NULL AND direction = 'OUTBOUND' AND type IN ('PIX', 'RETURN') AND created_at < $1
		ORDER BY created_at LIMIT $2`, before, limit)
}

func (p *Postgres) PendingBookTransfers(ctx context.Context, before time.Time, limit int) ([]Payment, error) {
	return p.many(ctx, `
		SELECT `+columns+` FROM payments
		WHERE status = 'PENDING' AND type = 'BOOK_TRANSFER' AND direction = 'OUTBOUND' AND created_at < $1
		ORDER BY created_at LIMIT $2`, before, limit)
}

func (p *Postgres) UnreportedBookTransfers(ctx context.Context, before time.Time, limit int) ([]Payment, error) {
	return p.many(ctx, `
		SELECT `+columns+` FROM payments
		WHERE type = 'BOOK_TRANSFER' AND direction = 'OUTBOUND' AND status = 'SETTLED'
		  AND ((report_status = '' AND (report_sent_at IS NULL OR report_sent_at < $1))
		    OR (report_status = 'SENT' AND report_sent_at < $1))
		ORDER BY finished_at LIMIT $2`, before, limit)
}

func (p *Postgres) SetReport(ctx context.Context, id, status, reason string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE payments SET report_status = $2, report_reason = $3, report_sent_at = $4
		WHERE id = $1 AND report_status <> 'ACCEPTED'`, id, status, reason, at)
	return err
}

func (p *Postgres) RejectedReports(ctx context.Context, limit int) ([]Payment, error) {
	return p.many(ctx, `
		SELECT `+columns+` FROM payments WHERE type = 'BOOK_TRANSFER' AND direction = 'OUTBOUND' AND report_status = 'REJECTED'
		ORDER BY report_sent_at LIMIT $1`, limit)
}

func (p *Postgres) ResetReport(ctx context.Context, id string) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE payments SET report_status = '', report_reason = '', report_sent_at = NULL
		WHERE id = $1 AND type = 'BOOK_TRANSFER' AND report_status = 'REJECTED'`, id)
	if isCode(err, "22P02") {
		return false, nil
	}
	return tag.RowsAffected() > 0, err
}

func (p *Postgres) List(ctx context.Context, q Query) ([]Payment, error) {
	statuses := make([]string, len(q.Statuses))
	for i, s := range q.Statuses {
		statuses[i] = string(s)
	}
	var afterAt *time.Time
	var afterID *string
	if q.After != nil {
		afterAt, afterID = &q.After.CreatedAt, &q.After.ID
	}
	return p.many(ctx, `
		SELECT `+columns+` FROM payments
		WHERE account_id = $1
		  AND (cardinality($2::text[]) = 0 OR status = ANY($2::text[]))
		  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $5`, q.AccountID, statuses, afterAt, afterID, q.Limit)
}

func (p *Postgres) many(ctx context.Context, query string, args ...any) ([]Payment, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payment
	for rows.Next() {
		got, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, rows.Err()
}

func isCode(err error, code string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == code
}
