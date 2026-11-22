package payment

import (
	"context"
	"errors"
	"testing"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

func (f *fixture) allEntries(t *testing.T, account string, pageSize int) []Entry {
	t.Helper()
	var all []Entry
	token := ""
	for range 20 {
		st, err := f.svc.Statement(context.Background(), StatementInput{AccountID: account, PageSize: pageSize, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, st.Entries...)
		if token = st.NextToken; token == "" {
			return all
		}
	}
	t.Fatal("the statement never ended")
	return nil
}

func TestStatement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	settledOut := f.pay(t, f.resolve(t), 2590)
	if err := f.svc.OnStatus(ctx, settled(settledOut.EndToEndID)); err != nil {
		t.Fatal(err)
	}
	rejectedOut := f.pay(t, f.resolve(t), 700)
	if err := f.svc.OnStatus(ctx, rejected(rejectedOut.EndToEndID, "AC07")); err != nil {
		t.Fatal(err)
	}
	f.pay(t, f.resolve(t), 300) // still pending: reserved, not moved
	book := f.pay(t, f.resolveBook(t), 400)
	orig := f.receivedAndSettled(t)
	ret := f.giveBack(t, orig, 200)
	if err := f.svc.OnStatus(ctx, returnStatus(ret.EndToEndID, pix.StatusSettled, "")); err != nil {
		t.Fatal(err)
	}

	type want struct {
		credit  bool
		cents   int64
		payment string
		name    string
	}
	expected := []want{
		{false, 200, ret.ID, "Carlos Mendes"},
		{true, 1050, orig.ID, "Carlos Mendes"},
		{false, 400, book.ID, "Bruno Lima"},
		{false, 2590, settledOut.ID, "Carla Dias"},
		{true, 100000, "", ""},
	}
	for _, size := range []int{0, 2, 1} {
		got := f.allEntries(t, anaID, size)
		if len(got) != len(expected) {
			t.Fatalf("page size %d: %d entries: %+v", size, len(got), got)
		}
		for i, w := range expected {
			e := got[i]
			if e.Credit != w.credit || e.AmountCents != w.cents || e.PaymentID != w.payment || e.CounterpartyName != w.name || e.ID == "" || e.CreatedAt.IsZero() {
				t.Errorf("page size %d, entry %d = %+v, want %+v", size, i, e, w)
			}
		}
	}

	var net int64
	for _, e := range f.allEntries(t, anaID, 0) {
		if e.Credit {
			net += e.AmountCents
		} else {
			net -= e.AmountCents
		}
	}
	b, _ := f.ledger.Balance(ctx, anaID)
	if net != b.AvailableCents+b.ReservedCents {
		t.Errorf("the entries add up to %d, the balance to %d", net, b.AvailableCents+b.ReservedCents)
	}

	bruno := f.allEntries(t, brunoID, 0)
	if len(bruno) != 2 || !bruno[0].Credit || bruno[0].AmountCents != 400 || bruno[0].CounterpartyName != "Ana Souza" || bruno[0].PaymentID == "" || bruno[0].PaymentID == book.ID {
		t.Errorf("Bruno's entries = %+v", bruno)
	}
}

func TestStatementValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for name, in := range map[string]StatementInput{
		"no account":    {},
		"page too big":  {AccountID: anaID, PageSize: 101},
		"negative page": {AccountID: anaID, PageSize: -1},
		"garbage token": {AccountID: anaID, PageToken: "garbage"},
		"zero in token": {AccountID: anaID, PageToken: encodeStatementToken(0)},
		"text in token": {AccountID: anaID, PageToken: "YWJj"},
	} {
		if _, err := f.svc.Statement(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.Statement(ctx, StatementInput{AccountID: "0a1b2c3d-9999-4000-8000-000000000009"}); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	if st, err := f.svc.Statement(ctx, StatementInput{AccountID: brunoID, PageSize: 100}); err != nil || len(st.Entries) != 1 || st.NextToken != "" {
		t.Errorf("Bruno has only his deposit: %+v, %v", st, err)
	}
}

func TestStatementEmpty(t *testing.T) {
	f := newFixture(t)
	if _, err := f.ledger.Open(context.Background(), ledger.Account{ID: "0a1b2c3d-0003-4000-8000-000000000003", Branch: "0001", Number: "00345678", HolderName: "Padaria", TaxID: "11222333000181"}); err != nil {
		t.Fatal(err)
	}
	st, err := f.svc.Statement(context.Background(), StatementInput{AccountID: "0a1b2c3d-0003-4000-8000-000000000003"})
	if err != nil || len(st.Entries) != 0 || st.NextToken != "" {
		t.Fatalf("%+v, %v", st, err)
	}
}
