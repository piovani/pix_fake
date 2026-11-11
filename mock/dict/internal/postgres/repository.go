// Package postgres implementa dict.Repository sobre PostgreSQL.
package postgres

import (
	"context"
	"errors"

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
	owner_type, owner_tax_id, owner_name, owner_trade_name, creation_date, key_ownership_date`

func (r *Repository) Create(ctx context.Context, e dict.Entry, requestID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO entries (key, key_type, participant, branch, account_number, account_type, opening_date,
			owner_type, owner_tax_id, owner_name, owner_trade_name, request_id, creation_date, key_ownership_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		e.Key, e.KeyType, e.Account.Participant, e.Account.Branch, e.Account.AccountNumber, e.Account.Type,
		e.Account.OpeningDate, e.Owner.Type, e.Owner.TaxID, e.Owner.Name, e.Owner.TradeName,
		requestID, e.CreationDate, e.KeyOwnershipDate)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		if pgErr.ConstraintName == "entries_participant_request_id_key" {
			return dict.ErrRequestIDUsed
		}
		return dict.ErrKeyExists
	}
	return err
}

func (r *Repository) Get(ctx context.Context, key string) (dict.Entry, error) {
	return r.queryOne(ctx, `SELECT `+selectColumns+` FROM entries WHERE key = $1`, key)
}

func (r *Repository) GetByRequestID(ctx context.Context, participant, requestID string) (dict.Entry, error) {
	return r.queryOne(ctx, `SELECT `+selectColumns+` FROM entries WHERE participant = $1 AND request_id = $2`,
		participant, requestID)
}

func (r *Repository) queryOne(ctx context.Context, sql string, args ...any) (dict.Entry, error) {
	var (
		e                           dict.Entry
		keyType, accType, ownerType string
	)
	err := r.pool.QueryRow(ctx, sql, args...).Scan(
		&e.Key, &keyType, &e.Account.Participant, &e.Account.Branch, &e.Account.AccountNumber, &accType,
		&e.Account.OpeningDate, &ownerType, &e.Owner.TaxID, &e.Owner.Name, &e.Owner.TradeName,
		&e.CreationDate, &e.KeyOwnershipDate)
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
	tag, err := r.pool.Exec(ctx, `
		UPDATE entries SET branch = $2, account_number = $3, account_type = $4, opening_date = $5,
			owner_name = $6, owner_trade_name = $7
		WHERE key = $1`,
		e.Key, e.Account.Branch, e.Account.AccountNumber, e.Account.Type, e.Account.OpeningDate,
		e.Owner.Name, e.Owner.TradeName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return dict.ErrNotFound
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, key string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM entries WHERE key = $1`, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return dict.ErrNotFound
	}
	return nil
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
