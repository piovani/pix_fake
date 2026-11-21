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
		INSERT INTO resolutions (account_id, key, end_to_end_id, payee, expires_at) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, r.AccountID, r.Key, r.EndToEndID, payee, r.ExpiresAt).Scan(&r.ID)
	return r, err
}

func (p *Postgres) Resolution(ctx context.Context, id string) (Resolution, bool, error) {
	var r Resolution
	var payee []byte
	err := p.pool.QueryRow(ctx, `
		SELECT id, account_id, key, end_to_end_id, payee, expires_at FROM resolutions WHERE id = $1`, id).
		Scan(&r.ID, &r.AccountID, &r.Key, &r.EndToEndID, &payee, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") { // not found, or not a UUID
		return Resolution{}, false, nil
	}
	if err != nil {
		return Resolution{}, false, err
	}
	r.Payee, err = unmarshalPayee(payee)
	return r, err == nil, err
}

const columns = `id, account_id, direction, type, status, amount_cents, end_to_end_id, resolution_id, reservation_id,
	counterparty, message, reject_code, reject_reason, resource_id, submitted_at, created_at, finished_at`

func scan(row pgx.Row) (Payment, error) {
	var p Payment
	var direction, typ, status string
	var resolution, reservation, resource *string
	var counterparty []byte
	var submitted, finished *time.Time
	err := row.Scan(&p.ID, &p.AccountID, &direction, &typ, &status, &p.AmountCents, &p.EndToEndID, &resolution, &reservation,
		&counterparty, &p.Message, &p.RejectCode, &p.RejectReason, &resource, &submitted, &p.CreatedAt, &finished)
	if err != nil {
		return Payment{}, err
	}
	p.Direction, p.Type, p.Status = Direction(direction), Type(typ), Status(status)
	p.ResolutionID, p.ReservationID, p.ResourceID = deref(resolution), deref(reservation), deref(resource)
	if submitted != nil {
		p.SubmittedAt = *submitted
	}
	if finished != nil {
		p.FinishedAt = *finished
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
			counterparty, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (resolution_id) DO NOTHING
		RETURNING `+columns,
		in.AccountID, string(in.Direction), string(in.Type), string(in.Status), in.AmountCents, in.EndToEndID,
		nullable(in.ResolutionID), nullable(in.ReservationID), cp, in.Message, in.CreatedAt))
	if errors.Is(err, pgx.ErrNoRows) { // the resolution already made a payment
		existing, found, err := p.ByResolution(ctx, in.ResolutionID)
		if err == nil && !found {
			err = pgx.ErrNoRows
		}
		return existing, false, err
	}
	return stored, err == nil, err
}

func (p *Postgres) one(ctx context.Context, where string, arg any) (Payment, bool, error) {
	got, err := scan(p.pool.QueryRow(ctx, `SELECT `+columns+` FROM payments WHERE `+where, arg))
	if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") {
		return Payment{}, false, nil
	}
	return got, err == nil, err
}

func (p *Postgres) ByID(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `id = $1`, id)
}
func (p *Postgres) ByEndToEnd(ctx context.Context, e string) (Payment, bool, error) {
	return p.one(ctx, `end_to_end_id = $1`, e)
}
func (p *Postgres) ByResolution(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `resolution_id = $1`, id)
}
func (p *Postgres) ByResource(ctx context.Context, id string) (Payment, bool, error) {
	return p.one(ctx, `resource_id = $1`, id)
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
		WHERE status = 'PENDING' AND submitted_at IS NULL AND direction = 'OUTBOUND' AND type = 'PIX' AND created_at < $1
		ORDER BY created_at LIMIT $2`, before, limit)
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
