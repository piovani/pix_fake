package pix

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// These tests need a running DICT (`make setup`) and WALLET_TEST_DICT_URL, such as http://localhost:8080/api/v2.
func liveClient(t *testing.T) *DictClient {
	t.Helper()
	base := os.Getenv("WALLET_TEST_DICT_URL")
	if base == "" {
		t.Skip("WALLET_TEST_DICT_URL is not set")
	}
	return NewDictClient(base, "99000001")
}

func TestLiveLookupOfAnotherBanksKey(t *testing.T) {
	c := liveClient(t)
	e2e, err := NewEndToEndID("99000001", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.GetEntry(context.Background(), GetEntryParams{Key: "+5521999990003", PayerID: "52998224725", EndToEndID: e2e})
	if err != nil {
		t.Fatal(err)
	}
	if e.Owner.Name != "Carlos Mendes" || e.Account.Participant != "99000002" || e.Account.AccountNumber != "00456789" {
		t.Fatalf("entry: %+v", e)
	}
}

func TestLiveErrors(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	e2e, _ := NewEndToEndID("99000001", time.Now())

	_, err := c.GetEntry(ctx, GetEntryParams{Key: "00000000000", PayerID: "52998224725", EndToEndID: e2e})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown key: %v", err)
	}
	_, err = c.GetEntry(ctx, GetEntryParams{Key: "11144477735", PayerID: "52998224725", EndToEndID: e2e}) // Bruno, a wallet customer
	if !errors.Is(err, ErrBookTransfer) {
		t.Errorf("the wallet's own key: %v", err)
	}
	_, err = c.GetEntry(ctx, GetEntryParams{Key: "39053344705", PayerID: "52998224725", EndToEndID: "not-an-id"})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("malformed EndToEndId: %v", err)
	}
}

func TestLiveRegisterAndRemoveAKey(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	reqID, _ := NewRequestID()
	e := Entry{
		KeyType: KeyEVP,
		Account: Account{Participant: "99000001", Branch: "0001", AccountNumber: "00234567", AccountType: AccountSavings,
			OpeningDate: time.Date(2020, 1, 1, 3, 0, 0, 0, time.UTC)},
		Owner: Owner{Type: NaturalPerson, TaxIDNumber: "11144477735", Name: "Bruno Lima"},
	}

	created, err := c.CreateEntry(ctx, e, reqID)
	if err != nil || created.Key == "" {
		t.Fatalf("create: %+v, %v", created, err)
	}
	t.Cleanup(func() { _ = c.DeleteEntry(ctx, created.Key, "") })

	again, err := c.CreateEntry(ctx, e, reqID) // same RequestId and data: the same entry, not a new key
	if err != nil || again.Key != created.Key {
		t.Fatalf("retry with the same RequestId: %+v, %v", again, err)
	}

	e.Owner.Name = "Other Name"
	if _, err := c.CreateEntry(ctx, e, reqID); !errors.Is(err, ErrRequestIDAlreadyUsed) {
		t.Errorf("same RequestId with other data: %v", err)
	}

	st, err := c.CheckKeys(ctx, []string{created.Key, "00000000000"})
	if err != nil || len(st) != 2 || !st[0].HasEntry || st[1].HasEntry {
		t.Fatalf("check: %+v, %v", st, err)
	}

	if err := c.DeleteEntry(ctx, created.Key, ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.DeleteEntry(ctx, created.Key, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
}
