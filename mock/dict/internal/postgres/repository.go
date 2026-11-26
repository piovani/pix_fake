// Package postgres implements dict.Repository on PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/dict/internal/dict"
)

const uniqueViolation = "23505"

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const selectColumns = `key, key_type, participant, branch, account_number, account_type, opening_date,
	owner_type, owner_tax_id, owner_name, owner_trade_name, creation_date, key_ownership_date, request_id::text,
	COALESCE(cid, '')`

// addEvent records a change to the CIDs, in the transaction of the change.
func addEvent(ctx context.Context, tx pgx.Tx, participant string, kt dict.KeyType, typ dict.CIDEventType, cid string) error {
	_, err := tx.Exec(ctx, `INSERT INTO cid_events (participant, key_type, type, cid) VALUES ($1, $2, $3, $4)`,
		participant, kt, typ, cid)
	return err
}

func (r *Repository) Create(ctx context.Context, e dict.Entry, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO entries (key, key_type, participant, branch, account_number, account_type, opening_date,
			owner_type, owner_tax_id, owner_name, owner_trade_name, request_id, creation_date, key_ownership_date, cid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		e.Key, e.KeyType, e.Account.Participant, e.Account.Branch, e.Account.AccountNumber, e.Account.Type,
		e.Account.OpeningDate, e.Owner.Type, e.Owner.TaxID, e.Owner.Name, e.Owner.TradeName,
		requestID, e.CreationDate, e.KeyOwnershipDate, e.CID)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		if pgErr.ConstraintName == "entries_participant_request_id_key" {
			return dict.ErrRequestIDUsed
		}
		return dict.ErrKeyExists
	}
	if err != nil {
		return err
	}
	if err := addEvent(ctx, tx, e.Account.Participant, e.KeyType, dict.CIDAdded, e.CID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) Get(ctx context.Context, key string) (dict.Entry, error) {
	return r.queryOne(ctx, `SELECT `+selectColumns+` FROM entries WHERE key = $1`, key)
}

func (r *Repository) GetByRequestID(ctx context.Context, participant, requestID string) (dict.Entry, error) {
	return r.queryOne(ctx, `SELECT `+selectColumns+` FROM entries WHERE participant = $1 AND request_id = $2`,
		participant, requestID)
}

func (r *Repository) queryOne(ctx context.Context, sql string, args ...any) (dict.Entry, error) {
	return scanEntry(r.pool.QueryRow(ctx, sql, args...))
}

// scanEntry reads one entry from a row of selectColumns; no row is dict.ErrNotFound.
func scanEntry(row pgx.Row) (dict.Entry, error) {
	var (
		e                           dict.Entry
		keyType, accType, ownerType string
	)
	err := row.Scan(
		&e.Key, &keyType, &e.Account.Participant, &e.Account.Branch, &e.Account.AccountNumber, &accType,
		&e.Account.OpeningDate, &ownerType, &e.Owner.TaxID, &e.Owner.Name, &e.Owner.TradeName,
		&e.CreationDate, &e.KeyOwnershipDate, &e.RequestID, &e.CID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.Entry{}, dict.ErrNotFound
	}
	if err != nil {
		return dict.Entry{}, err
	}
	e.KeyType, e.Account.Type, e.Owner.Type = dict.KeyType(keyType), dict.AccountType(accType), dict.PersonType(ownerType)
	e.Account.OpeningDate = e.Account.OpeningDate.UTC()
	e.CreationDate, e.KeyOwnershipDate = e.CreationDate.UTC(), e.KeyOwnershipDate.UTC()
	return e, nil
}

func (r *Repository) Update(ctx context.Context, e dict.Entry) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var participant, keyType, oldCID string
	err = tx.QueryRow(ctx, `SELECT participant, key_type, COALESCE(cid, '') FROM entries WHERE key = $1 FOR UPDATE`, e.Key).
		Scan(&participant, &keyType, &oldCID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE entries SET branch = $2, account_number = $3, account_type = $4, opening_date = $5,
			owner_name = $6, owner_trade_name = $7, cid = $8
		WHERE key = $1`,
		e.Key, e.Account.Branch, e.Account.AccountNumber, e.Account.Type, e.Account.OpeningDate,
		e.Owner.Name, e.Owner.TradeName, e.CID); err != nil {
		return err
	}
	kt := dict.KeyType(keyType)
	if err := addEvent(ctx, tx, participant, kt, dict.CIDRemoved, oldCID); err != nil {
		return err
	}
	if err := addEvent(ctx, tx, participant, kt, dict.CIDAdded, e.CID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) Delete(ctx context.Context, key string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var participant, keyType, cid string
	err = tx.QueryRow(ctx, `DELETE FROM entries WHERE key = $1 RETURNING participant, key_type, COALESCE(cid, '')`, key).
		Scan(&participant, &keyType, &cid)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := addEvent(ctx, tx, participant, dict.KeyType(keyType), dict.CIDRemoved, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteAll removes every entry, what the reconciliation kept about them, and every MED record (control API).
func (r *Repository) DeleteAll(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `TRUNCATE entries, cid_events, sync_verifications, cid_files, transactions, infraction_reports, fraud_markers, refunds RESTART IDENTITY`)
	return err
}

// ListEntries returns the entries, those of one participant when it is not empty, ordered by key (control API).
func (r *Repository) ListEntries(ctx context.Context, participant string, limit int) ([]dict.Entry, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := r.pool.Query(ctx, `SELECT `+selectColumns+` FROM entries WHERE ($1 = '' OR participant = $1) ORDER BY key LIMIT $2`,
		participant, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dict.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) Existing(ctx context.Context, keys []string) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT key FROM entries WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool, len(keys))
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

func (r *Repository) CIDs(ctx context.Context, participant string, kt dict.KeyType) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT cid FROM entries WHERE participant = $1 AND key_type = $2 ORDER BY cid`, participant, kt)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (r *Repository) GetByCID(ctx context.Context, cid string) (dict.Entry, error) {
	return r.queryOne(ctx, `SELECT `+selectColumns+` FROM entries WHERE cid = $1`, cid)
}

func (r *Repository) CIDEvents(ctx context.Context, participant string, kt dict.KeyType) ([]dict.CIDEvent, error) {
	rows, err := r.pool.Query(ctx, `SELECT type, cid, at FROM cid_events WHERE participant = $1 AND key_type = $2 ORDER BY seq`,
		participant, kt)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.CIDEvent, error) {
		var ev dict.CIDEvent
		var typ string
		err := row.Scan(&typ, &ev.CID, &ev.Timestamp)
		ev.Type, ev.Timestamp = dict.CIDEventType(typ), ev.Timestamp.UTC()
		return ev, err
	})
}

func (r *Repository) CreateSyncVerification(ctx context.Context, v dict.SyncVerification) (dict.SyncVerification, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sync_verifications (participant, key_type, participant_verifier, dict_verifier, result)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		v.Participant, v.KeyType, v.Verifier, v.DictVSync, v.Result).Scan(&v.ID)
	return v, err
}

func (r *Repository) CreateCIDFile(ctx context.Context, f dict.CIDFile) (dict.CIDFile, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO cid_files (participant, key_type, status, request_time, creation_time, content, sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		f.Participant, f.KeyType, f.Status, f.RequestTime, f.CreationTime, f.Content, f.Sha256).Scan(&f.ID)
	return f, err
}

func (r *Repository) GetCIDFile(ctx context.Context, id int64) (dict.CIDFile, error) {
	var f dict.CIDFile
	var kt, status string
	var created *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, participant, key_type, status, request_time, creation_time, content, sha256 FROM cid_files WHERE id = $1`, id).
		Scan(&f.ID, &f.Participant, &kt, &status, &f.RequestTime, &created, &f.Content, &f.Sha256)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.CIDFile{}, dict.ErrNotFound
	}
	f.KeyType, f.Status, f.RequestTime = dict.KeyType(kt), dict.FileStatus(status), f.RequestTime.UTC()
	if created != nil {
		f.CreationTime = created.UTC()
	}
	return f, err
}
