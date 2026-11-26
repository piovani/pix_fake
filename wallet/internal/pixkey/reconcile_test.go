package pixkey

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"pixfake/wallet/internal/pix"
)

// mirror puts the wallet's keys in the fake DICT the way Register would have, so both sides start in agreement.
func mirror(t *testing.T, s *Service, d *fakeDict, st *memStore) {
	t.Helper()
	d.dir = map[string]dictEntry{}
	for _, k := range st.keys {
		acct := fakeAccounts{ana.ID: ana, padaria.ID: padaria}[k.AccountID]
		d.dir[k.Key] = dictEntry{entry: s.entry(RegisterInput{Type: k.Type, Key: k.Key}, acct), requestID: k.RequestID}
	}
}

func walletKeys() []Key {
	return []Key{
		{Key: "52998224725", Type: pix.KeyCPF, AccountID: ana.ID, RequestID: req1},
		{Key: "ana.souza@example.com", Type: pix.KeyEmail, AccountID: ana.ID, RequestID: req2},
		{Key: "contato@paoquente.example.com", Type: pix.KeyEmail, AccountID: padaria.ID, RequestID: "33333333-3333-4333-8333-333333333333"},
		{Key: "+5511999990001", Type: pix.KeyPhone, AccountID: ana.ID, RequestID: "44444444-4444-4444-8444-444444444444"},
	}
}

func TestCheckAgrees(t *testing.T) {
	s, d, st := setup()
	st.keys = walletKeys()
	mirror(t, s, d, st)

	findings, err := s.Check(context.Background())
	if err != nil || len(findings) != 0 {
		t.Fatalf("findings = %+v, %v", findings, err)
	}
	if len(d.syncs) != len(keyTypes) || d.files != 0 {
		t.Fatalf("%d sync verifications, %d files: every key type is verified, no file is needed", len(d.syncs), d.files)
	}
}

func TestCheckFindsEachDivergence(t *testing.T) {
	s, d, st := setup()
	st.keys = walletKeys()
	mirror(t, s, d, st)

	delete(d.dir, "ana.souza@example.com") // removed from the DICT behind the wallet's back
	changed := d.dir["contato@paoquente.example.com"]
	changed.entry.Owner.Name = "Padaria Nova Ltda" // updated in the DICT, not in the wallet
	d.dir["contato@paoquente.example.com"] = changed
	unknown := s.entry(RegisterInput{Type: pix.KeyEmail, Key: "lost@example.com"}, ana) // registered, then lost by the wallet
	d.dir["lost@example.com"] = dictEntry{entry: unknown, requestID: "55555555-5555-4555-8555-555555555555"}

	findings, err := s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Finding{}
	for _, f := range findings {
		got[f.Key] = f
	}
	want := map[string]Finding{
		"ana.souza@example.com":         {Kind: MissingInDict, Key: "ana.souza@example.com", Type: pix.KeyEmail, AccountID: ana.ID},
		"contato@paoquente.example.com": {Kind: DataDiffers, Key: "contato@paoquente.example.com", Type: pix.KeyEmail, AccountID: padaria.ID},
		"lost@example.com":              {Kind: OnlyInDict, Key: "lost@example.com", Type: pix.KeyEmail},
	}
	if len(findings) != len(want) || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("findings = %+v", findings)
	}
	if d.files != 1 {
		t.Fatalf("%d files: only the key type that differs needs one", d.files)
	}
}

func TestCheckDifferentRequestID(t *testing.T) {
	s, d, st := setup()
	st.keys = walletKeys()
	mirror(t, s, d, st)
	cpf := d.dir["52998224725"]
	cpf.requestID = "66666666-6666-4666-8666-666666666666" // the same data, created by another request
	d.dir["52998224725"] = cpf

	findings, err := s.Check(context.Background())
	if err != nil || len(findings) != 1 || findings[0].Kind != DataDiffers || findings[0].Key != "52998224725" {
		t.Fatalf("findings = %+v, %v", findings, err)
	}
}

func TestCheckDICTDown(t *testing.T) {
	s, d, st := setup()
	st.keys = walletKeys()
	d.checkErr = pix.ErrUnavailable
	if _, err := s.Check(context.Background()); !errors.Is(err, pix.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckUnknownAccount(t *testing.T) {
	s, _, st := setup()
	st.keys = []Key{{Key: "x@example.com", Type: pix.KeyEmail, AccountID: "nope", RequestID: req1}}
	if _, err := s.Check(context.Background()); err == nil {
		t.Fatal("a key of an account that does not exist was reconciled")
	}
}
