package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

// ListInput asks for a page of the payments of an account, newest first. PageSize 0 means 50; the most is 100.
type ListInput struct {
	AccountID string
	PageSize  int
	PageToken string
	Statuses  []Status // empty means all
}

type Page struct {
	Payments  []Payment
	NextToken string
}

func (s *Service) List(ctx context.Context, in ListInput) (Page, error) {
	if in.AccountID == "" {
		return Page{}, fmt.Errorf("%w: account required", ErrInvalid)
	}
	size := in.PageSize
	switch {
	case size < 0 || size > maxPageSize:
		return Page{}, fmt.Errorf("%w: page size must be 0..%d", ErrInvalid, maxPageSize)
	case size == 0:
		size = defaultPageSize
	}
	after, err := decodeToken(in.PageToken)
	if err != nil {
		return Page{}, err
	}
	if _, err := s.accounts.Account(ctx, in.AccountID); err != nil {
		return Page{}, err
	}

	// one more than asked, to know whether there is a next page
	rows, err := s.store.List(ctx, Query{AccountID: in.AccountID, Statuses: in.Statuses, After: after, Limit: size + 1})
	if err != nil {
		return Page{}, err
	}
	page := Page{Payments: rows}
	if len(rows) > size {
		page.Payments = rows[:size]
		last := page.Payments[size-1]
		page.NextToken = encodeToken(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func encodeToken(c Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

func decodeToken(token string) (*Cursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: bad page token", ErrInvalid)
	}
	at, id, ok := strings.Cut(string(raw), "|")
	t, terr := time.Parse(time.RFC3339Nano, at)
	if !ok || terr != nil || id == "" {
		return nil, fmt.Errorf("%w: bad page token", ErrInvalid)
	}
	return &Cursor{CreatedAt: t, ID: id}, nil
}
