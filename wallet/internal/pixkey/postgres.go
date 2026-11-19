package pixkey

import (
	"context"
	"errors"

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

const columns = `key, key_type, account_id, request_id, created_at`

func scan(row pgx.Row) (Key, error) {
	var k Key
	var typ string
	err := row.Scan(&k.Key, &typ, &k.AccountID, &k.RequestID, &k.CreatedAt)
	k.Type = pix.KeyType(typ)
	return k, err
}

func (p *Postgres) ByRequestID(ctx context.Context, requestID string) (Key, bool, error) {
	return p.one(ctx, `SELECT `+columns+` FROM pix_keys WHERE request_id = $1`, requestID)
}

func (p *Postgres) ByKey(ctx context.Context, key string) (Key, bool, error) {
	return p.one(ctx, `SELECT `+columns+` FROM pix_keys WHERE key = $1`, key)
}

func (p *Postgres) ByAccount(ctx context.Context, accountID string) ([]Key, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+columns+` FROM pix_keys WHERE account_id = $1 ORDER BY created_at, key`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []Key
	for rows.Next() {
		k, err := scan(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (p *Postgres) Delete(ctx context.Context, key string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM pix_keys WHERE key = $1`, key)
	return err
}

func (p *Postgres) one(ctx context.Context, query string, arg any) (Key, bool, error) {
	k, err := scan(p.pool.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, false, nil
	}
	return k, err == nil, err
}

func (p *Postgres) Insert(ctx context.Context, k Key) (Key, error) {
	stored, err := scan(p.pool.QueryRow(ctx, `
		INSERT INTO pix_keys (key, key_type, account_id, request_id) VALUES ($1, $2, $3, $4)
		RETURNING `+columns, k.Key, string(k.Type), k.AccountID, k.RequestID))
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		// the same request arrived twice at once, or the key belongs to another request
		if existing, found, err := p.ByRequestID(ctx, k.RequestID); err == nil && found {
			return existing, nil
		}
		return Key{}, ErrKeyTaken
	}
	return stored, err
}
