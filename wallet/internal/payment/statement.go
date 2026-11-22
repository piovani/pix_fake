package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"pixfake/wallet/internal/ledger"
)

type StatementInput struct {
	AccountID string
	PageSize  int
	PageToken string
}

// Entry is a movement of money. PaymentID and CounterpartyName are empty for a movement no payment explains, such as a deposit.
type Entry struct {
	ID               string
	Credit           bool
	AmountCents      int64
	PaymentID        string
	CounterpartyName string
	CreatedAt        time.Time
}

type Statement struct {
	Entries   []Entry
	NextToken string
}

// Payments in flight are not in it: their money is reserved, not moved.
func (s *Service) Statement(ctx context.Context, in StatementInput) (Statement, error) {
	if in.AccountID == "" {
		return Statement{}, fmt.Errorf("%w: account is required", ErrInvalid)
	}
	size := in.PageSize
	switch {
	case size < 0 || size > maxPageSize:
		return Statement{}, fmt.Errorf("%w: page size must be between 0 and %d", ErrInvalid, maxPageSize)
	case size == 0:
		size = defaultPageSize
	}
	before, err := decodeStatementToken(in.PageToken)
	if err != nil {
		return Statement{}, err
	}
	if _, err := s.accounts.Account(ctx, in.AccountID); err != nil {
		return Statement{}, err
	}

	moves, err := s.accounts.Movements(ctx, in.AccountID, before, size+1)
	if err != nil {
		return Statement{}, err
	}
	var out Statement
	if len(moves) > size {
		moves = moves[:size]
		out.NextToken = encodeStatementToken(moves[size-1].ID)
	}

	refs := make([]string, len(moves))
	for i, m := range moves {
		refs[i] = m.Reference
	}
	payments, err := s.store.ByReferences(ctx, in.AccountID, refs)
	if err != nil {
		return Statement{}, err
	}
	byRef := make(map[string]Payment, 2*len(payments))
	for _, p := range payments {
		byRef[p.EndToEndID] = p
		if p.RequestID != "" {
			byRef[p.RequestID] = p
		}
	}

	for _, m := range moves {
		e := Entry{ID: strconv.FormatInt(m.ID, 10), Credit: m.Kind == ledger.Credited, AmountCents: m.AmountCents, CreatedAt: m.CreatedAt}
		if p, ok := byRef[m.Reference]; ok {
			e.PaymentID, e.CounterpartyName = p.ID, p.Counterparty.Name
		}
		out.Entries = append(out.Entries, e)
	}
	return out, nil
}

func encodeStatementToken(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeStatementToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil {
		var id int64
		if id, err = strconv.ParseInt(string(raw), 10, 64); err == nil && id > 0 {
			return id, nil
		}
	}
	return 0, fmt.Errorf("%w: page token is not valid", ErrInvalid)
}
