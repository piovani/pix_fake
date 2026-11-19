package pixkey

import (
	"context"
	"errors"
	"testing"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

const (
	req1 = "11111111-1111-4111-8111-111111111111"
	req2 = "22222222-2222-4222-8222-222222222222"
)

type fakeAccounts map[string]ledger.Account

func (f fakeAccounts) Account(_ context.Context, id string) (ledger.Account, error) {
	a, ok := f[id]
	if !ok {
		return ledger.Account{}, ledger.ErrAccountNotFound
	}
	return a, nil
}

type fakeDict struct {
	calls  []pix.Entry
	reqIDs []string
	err    error
	evpKey string
}

func (f *fakeDict) CreateEntry(_ context.Context, e pix.Entry, requestID string) (pix.Entry, error) {
	f.calls = append(f.calls, e)
	f.reqIDs = append(f.reqIDs, requestID)
	if f.err != nil {
		return pix.Entry{}, f.err
	}
	if e.Key == "" {
		e.Key = f.evpKey
	}
	return e, nil
}

type memStore struct{ keys []Key }

func (m *memStore) ByRequestID(_ context.Context, id string) (Key, bool, error) {
	for _, k := range m.keys {
		if k.RequestID == id {
			return k, true, nil
		}
	}
	return Key{}, false, nil
}

func (m *memStore) ByKey(_ context.Context, key string) (Key, bool, error) {
	for _, k := range m.keys {
		if k.Key == key {
			return k, true, nil
		}
	}
	return Key{}, false, nil
}

func (m *memStore) Insert(_ context.Context, k Key) (Key, error) {
	k.CreatedAt = time.Now()
	m.keys = append(m.keys, k)
	return k, nil
}

var ana = ledger.Account{
	ID: "acc-ana", Branch: "0001", Number: "00123456", HolderName: "Ana Souza", TaxID: "52998224725", Type: "CACC",
	OpenedAt: time.Date(2020, 1, 1, 3, 0, 0, 0, time.UTC),
}

var padaria = ledger.Account{ID: "acc-padaria", Branch: "0001", Number: "00345678", HolderName: "Padaria Pão Quente Ltda", TaxID: "11222333000181", Type: "CACC"}

func setup() (*Service, *fakeDict, *memStore) {
	d := &fakeDict{evpKey: "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01"}
	st := &memStore{}
	return New(fakeAccounts{ana.ID: ana, padaria.ID: padaria}, d, st, "99000001"), d, st
}

func TestRegisterSendsTheAccountToTheDICTAndStoresTheKey(t *testing.T) {
	s, d, st := setup()

	k, err := s.Register(context.Background(), RegisterInput{AccountID: ana.ID, Type: pix.KeyEmail, Key: "ana.souza@example.com", RequestID: req1})
	if err != nil {
		t.Fatal(err)
	}
	if k.Key != "ana.souza@example.com" || k.Type != pix.KeyEmail || k.AccountID != ana.ID || len(st.keys) != 1 {
		t.Fatalf("key = %+v, stored = %d", k, len(st.keys))
	}
	if len(d.calls) != 1 || d.reqIDs[0] != req1 {
		t.Fatalf("DICT calls = %d, request ids = %v", len(d.calls), d.reqIDs)
	}
	e := d.calls[0]
	if e.Account.Participant != "99000001" || e.Account.Branch != "0001" || e.Account.AccountNumber != "00123456" ||
		e.Account.AccountType != pix.AccountChecking || !e.Account.OpeningDate.Equal(ana.OpenedAt) ||
		e.Owner.Type != pix.NaturalPerson || e.Owner.TaxIDNumber != "52998224725" || e.Owner.Name != "Ana Souza" {
		t.Fatalf("entry sent to the DICT: %+v", e)
	}
}

func TestRegisterRandomKeyUsesWhatTheDICTGenerates(t *testing.T) {
	s, d, _ := setup()
	k, err := s.Register(context.Background(), RegisterInput{AccountID: ana.ID, Type: pix.KeyEVP, RequestID: req1})
	if err != nil || k.Key != d.evpKey || d.calls[0].Key != "" {
		t.Fatalf("key = %+v, err = %v, sent key = %q", k, err, d.calls[0].Key)
	}
}

func TestRegisterLegalPerson(t *testing.T) {
	s, d, _ := setup()
	if _, err := s.Register(context.Background(), RegisterInput{AccountID: padaria.ID, Type: pix.KeyCNPJ, Key: "11222333000181", RequestID: req1}); err != nil {
		t.Fatal(err)
	}
	if d.calls[0].Owner.Type != pix.LegalPerson {
		t.Fatalf("owner = %+v", d.calls[0].Owner)
	}
}

func TestRegisterRepeatedRequestReturnsTheSameKeyWithoutCallingTheDICT(t *testing.T) {
	s, d, st := setup()
	in := RegisterInput{AccountID: ana.ID, Type: pix.KeyEVP, RequestID: req1}
	first, _ := s.Register(context.Background(), in)

	again, err := s.Register(context.Background(), in)
	if err != nil || again.Key != first.Key || len(d.calls) != 1 || len(st.keys) != 1 {
		t.Fatalf("again = %+v, err = %v, DICT calls = %d, stored = %d", again, err, len(d.calls), len(st.keys))
	}
}

func TestRegisterSameRequestWithOtherDataIsRefused(t *testing.T) {
	s, _, _ := setup()
	ctx := context.Background()
	if _, err := s.Register(ctx, RegisterInput{AccountID: ana.ID, Type: pix.KeyPhone, Key: "+5511999990001", RequestID: req1}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]RegisterInput{
		"other key":     {AccountID: ana.ID, Type: pix.KeyPhone, Key: "+5511999990009", RequestID: req1},
		"other account": {AccountID: padaria.ID, Type: pix.KeyPhone, Key: "+5511999990001", RequestID: req1},
		"other type":    {AccountID: ana.ID, Type: pix.KeyEmail, Key: "+5511999990001", RequestID: req1},
	} {
		if _, err := s.Register(ctx, in); !errors.Is(err, ErrRequestConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRegisterKeyAlreadyStoredIsTakenWithoutCallingTheDICT(t *testing.T) {
	s, d, _ := setup()
	ctx := context.Background()
	if _, err := s.Register(ctx, RegisterInput{AccountID: ana.ID, Type: pix.KeyEmail, Key: "a@example.com", RequestID: req1}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Register(ctx, RegisterInput{AccountID: padaria.ID, Type: pix.KeyEmail, Key: "a@example.com", RequestID: req2})
	if !errors.Is(err, ErrKeyTaken) || len(d.calls) != 1 {
		t.Fatalf("err = %v, DICT calls = %d", err, len(d.calls))
	}
}

func TestRegisterDICTRefusals(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"key registered elsewhere", &pix.APIError{Status: 409, Kind: "Conflict"}, ErrKeyTaken},
		{"request id used", &pix.APIError{Status: 400, Kind: "RequestIdAlreadyUsed"}, ErrRequestConflict},
		{"invalid entry", &pix.APIError{Status: 400, Kind: "EntryInvalid"}, ErrInvalid},
	}
	for _, tt := range tests {
		s, d, st := setup()
		d.err = tt.err
		_, err := s.Register(context.Background(), RegisterInput{AccountID: ana.ID, Type: pix.KeyPhone, Key: "+5511999990001", RequestID: req1})
		if !errors.Is(err, tt.want) || len(st.keys) != 0 {
			t.Errorf("%s: err = %v, stored = %d", tt.name, err, len(st.keys))
		}
	}
}

func TestRegisterRetryAfterTheDICTWasDown(t *testing.T) {
	s, d, st := setup()
	in := RegisterInput{AccountID: ana.ID, Type: pix.KeyPhone, Key: "+5511999990001", RequestID: req1}

	d.err = &pix.APIError{Status: 503, Kind: "ServiceUnavailable"}
	if _, err := s.Register(context.Background(), in); !errors.Is(err, pix.ErrUnavailable) || len(st.keys) != 0 {
		t.Fatalf("down: err = %v, stored = %d", err, len(st.keys))
	}
	d.err = nil
	if _, err := s.Register(context.Background(), in); err != nil || len(st.keys) != 1 {
		t.Fatalf("retry: err = %v, stored = %d", err, len(st.keys))
	}
	if d.reqIDs[0] != d.reqIDs[1] {
		t.Fatalf("the retry must reuse the request id: %v", d.reqIDs)
	}
}

func TestRegisterValidation(t *testing.T) {
	s, d, _ := setup()
	tests := map[string]RegisterInput{
		"no account":               {Type: pix.KeyEmail, Key: "a@b.c", RequestID: req1},
		"request id not a uuid":    {AccountID: ana.ID, Type: pix.KeyEmail, Key: "a@b.c", RequestID: "abc"},
		"request id not v4":        {AccountID: ana.ID, Type: pix.KeyEmail, Key: "a@b.c", RequestID: "11111111-1111-1111-8111-111111111111"},
		"no type":                  {AccountID: ana.ID, Key: "a@b.c", RequestID: req1},
		"no key":                   {AccountID: ana.ID, Type: pix.KeyEmail, RequestID: req1},
		"evp with a key":           {AccountID: ana.ID, Type: pix.KeyEVP, Key: "x", RequestID: req1},
		"cpf of someone else":      {AccountID: ana.ID, Type: pix.KeyCPF, Key: "11144477735", RequestID: req1},
		"cnpj on a natural person": {AccountID: ana.ID, Type: pix.KeyCNPJ, Key: "11222333000181", RequestID: req1},
	}
	for name, in := range tests {
		if _, err := s.Register(context.Background(), in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(d.calls) != 0 {
		t.Errorf("the DICT was called %d times for invalid requests", len(d.calls))
	}

	if _, err := s.Register(context.Background(), RegisterInput{AccountID: "nope", Type: pix.KeyEmail, Key: "a@b.c", RequestID: req1}); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Errorf("unknown account: %v", err)
	}
}

func TestRegisterOwnCPFKey(t *testing.T) {
	s, _, _ := setup()
	if _, err := s.Register(context.Background(), RegisterInput{AccountID: ana.ID, Type: pix.KeyCPF, Key: "52998224725", RequestID: req1}); err != nil {
		t.Fatal(err)
	}
}
