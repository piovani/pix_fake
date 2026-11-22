// Package pixkey registers the Pix keys of the wallet's accounts in the DICT and keeps the wallet's own copy of them, which
// is how it recognizes a key of its own when a payment is a book transfer.
package pixkey

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

var (
	ErrInvalid         = errors.New("pixkey: invalid request")
	ErrKeyTaken        = errors.New("pixkey: key already registered")
	ErrRequestConflict = errors.New("pixkey: request id already used with other data")
	ErrKeyNotFound     = errors.New("pixkey: key not found")
)

type Key struct {
	Key       string
	Type      pix.KeyType
	AccountID string
	RequestID string
	CreatedAt time.Time
}

type RegisterInput struct {
	AccountID string
	Type      pix.KeyType
	Key       string // empty for an EVP key: the DICT generates it
	RequestID string // UUID v4, chosen by the caller
}

type Accounts interface {
	Account(ctx context.Context, id string) (ledger.Account, error)
}

type Directory interface {
	CreateEntry(ctx context.Context, e pix.Entry, requestID string) (pix.Entry, error)
	DeleteEntry(ctx context.Context, key string, reason pix.EntryReason) error
}

// Insert returns the existing key when the request id was already stored, and ErrKeyTaken when the key belongs to another request.
type Store interface {
	ByRequestID(ctx context.Context, requestID string) (Key, bool, error)
	ByKey(ctx context.Context, key string) (Key, bool, error)
	ByAccount(ctx context.Context, accountID string) ([]Key, error)
	Insert(ctx context.Context, k Key) (Key, error)
	Delete(ctx context.Context, key string) error
}

type Service struct {
	accounts Accounts
	dict     Directory
	store    Store
	ispb     string
}

func New(accounts Accounts, dict Directory, store Store, ispb string) *Service {
	return &Service{accounts: accounts, dict: dict, store: store, ispb: ispb}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Repeating a call with the same RequestID and data returns the same key, even if the first stopped halfway.
func (s *Service) Register(ctx context.Context, in RegisterInput) (Key, error) {
	if err := validate(in); err != nil {
		return Key{}, err
	}

	if k, found, err := s.store.ByRequestID(ctx, in.RequestID); err != nil {
		return Key{}, err
	} else if found {
		return replay(k, in)
	}

	acct, err := s.accounts.Account(ctx, in.AccountID)
	if err != nil {
		return Key{}, err
	}
	if (in.Type == pix.KeyCPF || in.Type == pix.KeyCNPJ) && in.Key != acct.TaxID {
		return Key{}, fmt.Errorf("%w: %s key must match the holder", ErrInvalid, in.Type)
	}
	if in.Key != "" {
		if _, found, err := s.store.ByKey(ctx, in.Key); err != nil {
			return Key{}, err
		} else if found {
			return Key{}, ErrKeyTaken
		}
	}

	entry, err := s.dict.CreateEntry(ctx, s.entry(in, acct), in.RequestID)
	if err != nil {
		return Key{}, mapDictError(err)
	}
	return s.store.Insert(ctx, Key{Key: entry.Key, Type: in.Type, AccountID: acct.ID, RequestID: in.RequestID})
}

func (s *Service) AccountOf(ctx context.Context, key string) (accountID string, found bool, err error) {
	k, found, err := s.store.ByKey(ctx, key)
	return k.AccountID, found, err
}

func (s *Service) List(ctx context.Context, accountID string) ([]Key, error) {
	if accountID == "" {
		return nil, fmt.Errorf("%w: account required", ErrInvalid)
	}
	if _, err := s.accounts.Account(ctx, accountID); err != nil {
		return nil, err
	}
	return s.store.ByAccount(ctx, accountID)
}

// A key that is already gone counts as removed, so the call can be repeated; the DICT is asked first.
func (s *Service) Remove(ctx context.Context, accountID, key string) error {
	if accountID == "" || key == "" {
		return fmt.Errorf("%w: account and key required", ErrInvalid)
	}
	if _, err := s.accounts.Account(ctx, accountID); err != nil {
		return err
	}
	stored, found, err := s.store.ByKey(ctx, key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if stored.AccountID != accountID {
		return ErrKeyNotFound
	}
	if err := s.dict.DeleteEntry(ctx, key, pix.ReasonUserRequested); err != nil && !errors.Is(err, pix.ErrNotFound) {
		return mapDictError(err)
	}
	return s.store.Delete(ctx, key)
}

func validate(in RegisterInput) error {
	switch {
	case in.AccountID == "":
		return fmt.Errorf("%w: account required", ErrInvalid)
	case !uuidV4.MatchString(in.RequestID):
		return fmt.Errorf("%w: request id must be uuid v4", ErrInvalid)
	}
	switch in.Type {
	case pix.KeyEVP:
		if in.Key != "" {
			return fmt.Errorf("%w: key must be empty for EVP", ErrInvalid)
		}
	case pix.KeyCPF, pix.KeyCNPJ, pix.KeyPhone, pix.KeyEmail:
		if in.Key == "" {
			return fmt.Errorf("%w: key required for %s", ErrInvalid, in.Type)
		}
	default:
		return fmt.Errorf("%w: key type required", ErrInvalid)
	}
	return nil
}

func replay(stored Key, in RegisterInput) (Key, error) {
	same := stored.AccountID == in.AccountID && stored.Type == in.Type && (in.Type == pix.KeyEVP || stored.Key == in.Key)
	if !same {
		return Key{}, ErrRequestConflict
	}
	return stored, nil
}

func (s *Service) entry(in RegisterInput, a ledger.Account) pix.Entry {
	owner := pix.Owner{Type: pix.NaturalPerson, TaxIDNumber: a.TaxID, Name: a.HolderName}
	if len(a.TaxID) == 14 {
		owner.Type = pix.LegalPerson
	}
	return pix.Entry{
		Key:     in.Key,
		KeyType: in.Type,
		Account: pix.Account{
			Participant: s.ispb, Branch: a.Branch, AccountNumber: a.Number,
			AccountType: pix.AccountType(a.Type), OpeningDate: a.OpenedAt,
		},
		Owner: owner,
	}
}

// The rest (unavailable, rate limited) is left for the caller to retry.
func mapDictError(err error) error {
	switch {
	case errors.Is(err, pix.ErrConflict):
		return ErrKeyTaken
	case errors.Is(err, pix.ErrRequestIDAlreadyUsed):
		return ErrRequestConflict
	case errors.Is(err, pix.ErrBadRequest):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return err
}
