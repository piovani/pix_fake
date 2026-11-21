package ledger

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/wallet/internal/platform/db"
)

// Errors returned by the ledger. Callers compare them with errors.Is.
var (
	ErrAccountNotFound     = errors.New("ledger: account not found")
	ErrAccountExists       = errors.New("ledger: account already exists")
	ErrInsufficientFunds   = errors.New("ledger: insufficient funds")
	ErrInvalidAmount       = errors.New("ledger: amount must be positive")
	ErrInvalidReference    = errors.New("ledger: reference is required")
	ErrReservationNotFound = errors.New("ledger: reservation not found")
	ErrReservationClosed   = errors.New("ledger: reservation already closed the other way")
	ErrReferenceConflict   = errors.New("ledger: reference already used with a different account or amount")
	ErrSameAccount         = errors.New("ledger: a transfer needs two different accounts")
)

// Account is an account of the wallet. Branch and Number are what the DICT holds for its keys, so the wallet can
// recognize its own keys.
type Account struct {
	ID         string // empty on Open means "generate one"
	Branch     string
	Number     string
	HolderName string
	TaxID      string
	Type       string    // CACC (the default), SVGS or TRAN
	OpenedAt   time.Time // set when read
}

// Balance is the money of an account, in centavos. Available can be spent; Reserved is held by payments in flight.
type Balance struct {
	AvailableCents int64
	ReservedCents  int64
}

type ReservationStatus string

const (
	Held     ReservationStatus = "HELD"
	Settled  ReservationStatus = "SETTLED"
	Released ReservationStatus = "RELEASED"
)

// Reservation is money held for one payment until it is settled (the money leaves) or released (it comes back).
type Reservation struct {
	ID          string
	AccountID   string
	AmountCents int64
	Reference   string
	Status      ReservationStatus
}

type entryKind string

const (
	kindCredit  entryKind = "CREDIT"
	kindReserve entryKind = "RESERVE"
	kindSettle  entryKind = "SETTLE"
	kindRelease entryKind = "RELEASE"
	kindDebit   entryKind = "DEBIT"
)

// Ledger keeps the accounts, their balances and the log of movements in the wallet's database.
type Ledger struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Ledger { return &Ledger{pool: pool} }

// Open creates an account with a zero balance. It returns ErrAccountExists if the ID or the branch and number are taken.
func (l *Ledger) Open(ctx context.Context, a Account) (Account, error) {
	err := l.pool.QueryRow(ctx, `
		INSERT INTO accounts (id, branch, number, holder_name, tax_id, account_type)
		VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'CACC'))
		RETURNING id, account_type, created_at`,
		strings.ToLower(a.ID), a.Branch, a.Number, a.HolderName, a.TaxID, a.Type).Scan(&a.ID, &a.Type, &a.OpenedAt)
	if isCode(err, "23505") {
		return Account{}, ErrAccountExists
	}
	return a, err
}

// Account returns an account with its data.
func (l *Ledger) Account(ctx context.Context, accountID string) (Account, error) {
	var a Account
	err := l.pool.QueryRow(ctx, `
		SELECT id, branch, number, holder_name, tax_id, account_type, created_at FROM accounts WHERE id = $1`,
		strings.ToLower(accountID)).Scan(&a.ID, &a.Branch, &a.Number, &a.HolderName, &a.TaxID, &a.Type, &a.OpenedAt)
	if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") {
		return Account{}, ErrAccountNotFound
	}
	return a, err
}

func (l *Ledger) AccountByNumber(ctx context.Context, branch, number string) (Account, error) {
	var a Account
	err := l.pool.QueryRow(ctx, `
		SELECT id, branch, number, holder_name, tax_id, account_type, created_at FROM accounts WHERE branch = $1 AND number = $2`,
		branch, number).Scan(&a.ID, &a.Branch, &a.Number, &a.HolderName, &a.TaxID, &a.Type, &a.OpenedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	return a, err
}

// Balance returns the balance of an account.
func (l *Ledger) Balance(ctx context.Context, accountID string) (Balance, error) {
	var b Balance
	err := l.pool.QueryRow(ctx, `SELECT available_cents, reserved_cents FROM accounts WHERE id = $1`,
		strings.ToLower(accountID)).Scan(&b.AvailableCents, &b.ReservedCents)
	if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") {
		return Balance{}, ErrAccountNotFound
	}
	return b, err
}

// Credit adds money to the available balance: a deposit or a received payment. The reference (for a received payment,
// its EndToEndId) makes it idempotent: crediting the same reference twice credits once.
func (l *Ledger) Credit(ctx context.Context, accountID string, cents int64, reference string) error {
	if cents <= 0 {
		return ErrInvalidAmount
	}
	if reference == "" {
		return ErrInvalidReference
	}
	accountID = strings.ToLower(accountID)
	return db.InTx(ctx, l.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, kind, amount_cents, reference) VALUES ($1, $2, $3, $4)
			ON CONFLICT (account_id, kind, reference) DO NOTHING`, accountID, kindCredit, cents, reference)
		if err != nil {
			return mapAccountErr(err)
		}
		if tag.RowsAffected() == 0 {
			return nil // already credited
		}
		_, err = tx.Exec(ctx, `UPDATE accounts SET available_cents = available_cents + $1 WHERE id = $2`, cents, accountID)
		return err
	})
}

// Reserve holds money for a payment: it moves it from available to reserved. It returns ErrInsufficientFunds when the
// available balance is lower, without changing anything. The reference (the payment's EndToEndId) makes it idempotent:
// the same reference with the same account and amount returns the existing reservation and moves nothing.
func (l *Ledger) Reserve(ctx context.Context, accountID string, cents int64, reference string) (Reservation, error) {
	if cents <= 0 {
		return Reservation{}, ErrInvalidAmount
	}
	if reference == "" {
		return Reservation{}, ErrInvalidReference
	}
	accountID = strings.ToLower(accountID)

	var r Reservation
	err := db.InTx(ctx, l.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO reservations (account_id, amount_cents, reference, status) VALUES ($1, $2, $3, 'HELD')
			ON CONFLICT (reference) DO NOTHING
			RETURNING id, account_id, amount_cents, reference, status`, accountID, cents, reference).
			Scan(&r.ID, &r.AccountID, &r.AmountCents, &r.Reference, &r.Status)
		if errors.Is(err, pgx.ErrNoRows) { // already reserved: return it, as long as it is the same request
			err = tx.QueryRow(ctx, `SELECT id, account_id, amount_cents, reference, status FROM reservations WHERE reference = $1`,
				reference).Scan(&r.ID, &r.AccountID, &r.AmountCents, &r.Reference, &r.Status)
			if err != nil {
				return err
			}
			if r.AccountID != accountID || r.AmountCents != cents {
				return ErrReferenceConflict
			}
			return nil
		}
		if err != nil {
			return mapAccountErr(err)
		}

		// Atomic and conditional: two concurrent reservations can never spend the same money.
		tag, err := tx.Exec(ctx, `
			UPDATE accounts SET available_cents = available_cents - $1, reserved_cents = reserved_cents + $1
			WHERE id = $2 AND available_cents >= $1`, cents, accountID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInsufficientFunds // rolls back the reservation row too
		}
		return addEntry(ctx, tx, accountID, kindReserve, cents, reference)
	})
	if err != nil {
		return Reservation{}, err
	}
	return r, nil
}

// Transfer debits one account and credits another in one transaction. The reference makes it idempotent.
func (l *Ledger) Transfer(ctx context.Context, fromAccountID, toAccountID string, cents int64, reference string) error {
	if cents <= 0 {
		return ErrInvalidAmount
	}
	if reference == "" {
		return ErrInvalidReference
	}
	from, to := strings.ToLower(fromAccountID), strings.ToLower(toAccountID)
	if from == to {
		return ErrSameAccount
	}
	return db.InTx(ctx, l.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, kind, amount_cents, reference) VALUES ($1, $2, $3, $4)
			ON CONFLICT (account_id, kind, reference) DO NOTHING`, from, kindDebit, cents, reference)
		if err != nil {
			return mapAccountErr(err)
		}
		if tag.RowsAffected() == 0 { // already transferred: the same one, or a reference reused for something else
			var done int64
			if err := tx.QueryRow(ctx, `SELECT amount_cents FROM ledger_entries WHERE account_id = $1 AND kind = $2 AND reference = $3`,
				from, kindDebit, reference).Scan(&done); err != nil {
				return err
			}
			if done != cents {
				return ErrReferenceConflict
			}
			return nil
		}

		// the condition is what stops two transfers spending the same money
		tag, err = tx.Exec(ctx, `UPDATE accounts SET available_cents = available_cents - $1 WHERE id = $2 AND available_cents >= $1`, cents, from)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInsufficientFunds // rolls back the entry too
		}
		if err := addEntry(ctx, tx, to, kindCredit, cents, reference); err != nil {
			return mapAccountErr(err)
		}
		_, err = tx.Exec(ctx, `UPDATE accounts SET available_cents = available_cents + $1 WHERE id = $2`, cents, to)
		return err
	})
}

// Settle confirms a reservation: the reserved money leaves the account. Settling a reservation that is already settled
// does nothing; a released one returns ErrReservationClosed.
func (l *Ledger) Settle(ctx context.Context, reservationID string) error {
	return l.close(ctx, reservationID, Settled, kindSettle)
}

// Release gives a reservation back: the reserved money returns to the available balance (the payment was rejected or
// timed out). Releasing one already released does nothing; a settled one returns ErrReservationClosed.
func (l *Ledger) Release(ctx context.Context, reservationID string) error {
	return l.close(ctx, reservationID, Released, kindRelease)
}

func (l *Ledger) close(ctx context.Context, reservationID string, to ReservationStatus, kind entryKind) error {
	reservationID = strings.ToLower(reservationID)
	return db.InTx(ctx, l.pool, func(tx pgx.Tx) error {
		var accountID, reference string
		var cents int64
		err := tx.QueryRow(ctx, `
			UPDATE reservations SET status = $2, updated_at = now() WHERE id = $1 AND status = 'HELD'
			RETURNING account_id, amount_cents, reference`, reservationID, to).Scan(&accountID, &cents, &reference)
		if errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02") {
			var current ReservationStatus
			err := tx.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, reservationID).Scan(&current)
			switch {
			case errors.Is(err, pgx.ErrNoRows) || isCode(err, "22P02"):
				return ErrReservationNotFound
			case err != nil:
				return err
			case current == to:
				return nil // already done
			default:
				return ErrReservationClosed
			}
		}
		if err != nil {
			return err
		}

		// Settling takes the money out of the account; releasing puts it back into available.
		back := int64(0)
		if to == Released {
			back = cents
		}
		if _, err := tx.Exec(ctx, `
			UPDATE accounts SET reserved_cents = reserved_cents - $1, available_cents = available_cents + $2 WHERE id = $3`,
			cents, back, accountID); err != nil {
			return err
		}
		return addEntry(ctx, tx, accountID, kind, cents, reference)
	})
}

func addEntry(ctx context.Context, tx pgx.Tx, accountID string, kind entryKind, cents int64, reference string) error {
	_, err := tx.Exec(ctx, `INSERT INTO ledger_entries (account_id, kind, amount_cents, reference) VALUES ($1, $2, $3, $4)`,
		accountID, kind, cents, reference)
	return err
}

func isCode(err error, code string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == code
}

// mapAccountErr turns "the account does not exist" (a foreign key violation, or an ID that is not a UUID) into
// ErrAccountNotFound.
func mapAccountErr(err error) error {
	if isCode(err, "23503") || isCode(err, "22P02") {
		return ErrAccountNotFound
	}
	return err
}
