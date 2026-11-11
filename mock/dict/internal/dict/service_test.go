package dict_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/memstore"
)

const (
	psp       = "12345678"
	otherPSP  = "87654321"
	requestID = "a946d533-7f22-42a5-9a9b-e87cd55c0f4d"
)

func newService() *dict.Service { return dict.NewService(memstore.New()) }

func phoneEntry() dict.Entry {
	return dict.Entry{
		Key:     "+5561988880000",
		KeyType: dict.KeyPhone,
		Account: dict.Account{
			Participant: psp, Branch: "0001", AccountNumber: "0007654321",
			Type: dict.AccountChecking, OpeningDate: time.Date(2010, 1, 10, 3, 0, 0, 0, time.UTC),
		},
		Owner: dict.Owner{Type: dict.NaturalPerson, TaxID: "11122233300", Name: "João Silva"},
	}
}

func createInput(e dict.Entry) dict.CreateEntryInput {
	return dict.CreateEntryInput{Entry: e, Reason: dict.ReasonUserRequested, RequestID: requestID}
}

// wantKind checks that err is a *dict.Error of the expected kind.
func wantKind(t *testing.T, err error, kind dict.Kind) *dict.Error {
	t.Helper()
	var de *dict.Error
	if !errors.As(err, &de) || de.Kind != kind {
		t.Fatalf("error = %v, want kind %s", err, kind)
	}
	return de
}

func TestCreateEntry(t *testing.T) {
	ctx := context.Background()

	t.Run("creates and looks up", func(t *testing.T) {
		svc := newService()
		got, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry()))
		if err != nil {
			t.Fatal(err)
		}
		if got.CreationDate.IsZero() || !got.CreationDate.Equal(got.KeyOwnershipDate) {
			t.Fatalf("dates not set: %+v", got)
		}
		found, err := svc.GetEntry(ctx, "+5561988880000")
		if err != nil || found.Owner.Name != "João Silva" {
			t.Fatalf("GetEntry = %+v, %v", found, err)
		}
	})

	t.Run("EVP generates the key", func(t *testing.T) {
		e := phoneEntry()
		e.Key, e.KeyType = "", dict.KeyEVP
		got, err := newService().CreateEntry(ctx, psp, createInput(e))
		if err != nil {
			t.Fatal(err)
		}
		if kt, ok := dict.DetectKeyType(got.Key); !ok || kt != dict.KeyEVP {
			t.Fatalf("generated key %q is not EVP", got.Key)
		}
	})

	t.Run("EVP with a supplied key is invalid", func(t *testing.T) {
		e := phoneEntry()
		e.Key, e.KeyType = dict.NewEVP(), dict.KeyEVP
		_, err := newService().CreateEntry(ctx, psp, createInput(e))
		wantKind(t, err, dict.KindEntryInvalid)
	})

	t.Run("same RequestId and same data is idempotent", func(t *testing.T) {
		svc := newService()
		first, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry()))
		if err != nil {
			t.Fatal(err)
		}
		again, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry()))
		if err != nil || again != first {
			t.Fatalf("replay = %+v, %v; want %+v", again, err, first)
		}
	})

	t.Run("same RequestId with different data", func(t *testing.T) {
		svc := newService()
		if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil {
			t.Fatal(err)
		}
		other := phoneEntry()
		other.Account.AccountNumber = "999"
		_, err := svc.CreateEntry(ctx, psp, createInput(other))
		wantKind(t, err, dict.KindRequestIDAlreadyUsed)
	})

	t.Run("key already registered", func(t *testing.T) {
		svc := newService()
		if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil {
			t.Fatal(err)
		}
		in := createInput(phoneEntry())
		in.RequestID = "b946d533-7f22-42a5-9a9b-e87cd55c0f4d"
		_, err := svc.CreateEntry(ctx, psp, in)
		wantKind(t, err, dict.KindConflict)
	})

	t.Run("SLRY account", func(t *testing.T) {
		e := phoneEntry()
		e.Account.Type = dict.AccountSalary
		_, err := newService().CreateEntry(ctx, psp, createInput(e))
		wantKind(t, err, dict.KindAccountTypeInvalid)
	})

	t.Run("account of another participant", func(t *testing.T) {
		_, err := newService().CreateEntry(ctx, otherPSP, createInput(phoneEntry()))
		wantKind(t, err, dict.KindForbidden)
	})

	t.Run("invalid fields return violations", func(t *testing.T) {
		e := phoneEntry()
		e.Key = "61988887777" // without the +55
		in := createInput(e)
		in.Reason = dict.ReasonFraud
		_, err := newService().CreateEntry(ctx, psp, in)
		de := wantKind(t, err, dict.KindEntryInvalid)
		props := map[string]bool{}
		for _, v := range de.Violations {
			props[v.Property] = true
		}
		if !props["entry.key"] || !props["reason"] {
			t.Fatalf("violations = %+v, want entry.key and reason", de.Violations)
		}
	})

	t.Run("CPF key must belong to the owner", func(t *testing.T) {
		e := phoneEntry()
		e.Key, e.KeyType = "99999999999", dict.KeyCPF
		_, err := newService().CreateEntry(ctx, psp, createInput(e))
		wantKind(t, err, dict.KindEntryInvalid)
	})
}

func TestGetEntry(t *testing.T) {
	svc := newService()
	_, err := svc.GetEntry(context.Background(), "+5561988880000")
	wantKind(t, err, dict.KindNotFound)
	_, err = svc.GetEntry(context.Background(), "not-a-key")
	wantKind(t, err, dict.KindBadRequest)
}

func TestUpdateEntry(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) *dict.Service {
		svc := newService()
		if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil {
			t.Fatal(err)
		}
		return svc
	}
	key := phoneEntry().Key

	t.Run("updates account and name", func(t *testing.T) {
		acc := phoneEntry().Account
		acc.AccountNumber = "0001111111"
		owner := phoneEntry().Owner
		owner.Name = "João Souza"
		got, err := setup(t).UpdateEntry(ctx, psp, key, dict.UpdateEntryInput{
			Key: key, Account: &acc, Owner: &owner, Reason: dict.ReasonUserRequested,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Account.AccountNumber != "0001111111" || got.Owner.Name != "João Souza" {
			t.Fatalf("not updated: %+v", got)
		}
	})

	t.Run("does not change the participant", func(t *testing.T) {
		acc := phoneEntry().Account
		acc.Participant = otherPSP
		_, err := setup(t).UpdateEntry(ctx, psp, key, dict.UpdateEntryInput{Key: key, Account: &acc, Reason: dict.ReasonUserRequested})
		wantKind(t, err, dict.KindBadRequest)
	})

	t.Run("does not change the owner's tax id", func(t *testing.T) {
		owner := phoneEntry().Owner
		owner.TaxID = "99988877766"
		_, err := setup(t).UpdateEntry(ctx, psp, key, dict.UpdateEntryInput{Key: key, Owner: &owner, Reason: dict.ReasonUserRequested})
		wantKind(t, err, dict.KindBadRequest)
	})

	t.Run("another participant", func(t *testing.T) {
		_, err := setup(t).UpdateEntry(ctx, otherPSP, key, dict.UpdateEntryInput{Key: key, Reason: dict.ReasonUserRequested})
		wantKind(t, err, dict.KindForbidden)
	})

	t.Run("does not exist", func(t *testing.T) {
		_, err := newService().UpdateEntry(ctx, psp, key, dict.UpdateEntryInput{Key: key, Reason: dict.ReasonUserRequested})
		wantKind(t, err, dict.KindNotFound)
	})

	t.Run("body key differs from the URL", func(t *testing.T) {
		_, err := setup(t).UpdateEntry(ctx, psp, key, dict.UpdateEntryInput{Key: "+5561900000000", Reason: dict.ReasonUserRequested})
		wantKind(t, err, dict.KindBadRequest)
	})

	t.Run("EVP does not accept USER_REQUESTED", func(t *testing.T) {
		svc := newService()
		e := phoneEntry()
		e.Key, e.KeyType = "", dict.KeyEVP
		created, err := svc.CreateEntry(ctx, psp, createInput(e))
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.UpdateEntry(ctx, psp, created.Key, dict.UpdateEntryInput{Key: created.Key, Reason: dict.ReasonUserRequested})
		wantKind(t, err, dict.KindBadRequest)
		if _, err = svc.UpdateEntry(ctx, psp, created.Key, dict.UpdateEntryInput{Key: created.Key, Reason: dict.ReasonBranchTransfer}); err != nil {
			t.Fatalf("BRANCH_TRANSFER should be accepted: %v", err)
		}
	})
}

func TestDeleteEntry(t *testing.T) {
	ctx := context.Background()
	key := phoneEntry().Key
	in := dict.DeleteEntryInput{Key: key, Participant: psp, Reason: dict.ReasonAccountClosure}

	svc := newService()
	if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil {
		t.Fatal(err)
	}

	forbidden := in
	forbidden.Participant = otherPSP
	wantKind(t, svc.DeleteEntry(ctx, otherPSP, key, forbidden), dict.KindForbidden)
	wantKind(t, svc.DeleteEntry(ctx, otherPSP, key, in), dict.KindForbidden) // caller != Participant in the body

	invalidReason := in
	invalidReason.Reason = dict.ReasonBranchTransfer
	wantKind(t, svc.DeleteEntry(ctx, psp, key, invalidReason), dict.KindBadRequest)

	if err := svc.DeleteEntry(ctx, psp, key, in); err != nil {
		t.Fatal(err)
	}
	wantKind(t, svc.DeleteEntry(ctx, psp, key, in), dict.KindNotFound)
}

func TestCheckKeys(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil {
		t.Fatal(err)
	}

	got, err := svc.CheckKeys(ctx, []string{"+5561988880000", "mail@mail.com", "+5561988880000"})
	if err != nil {
		t.Fatal(err)
	}
	want := []dict.KeyStatus{{Key: "+5561988880000", HasEntry: true}, {Key: "mail@mail.com"}, {Key: "+5561988880000", HasEntry: true}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	_, err = svc.CheckKeys(ctx, nil)
	wantKind(t, err, dict.KindBadRequest)
	_, err = svc.CheckKeys(ctx, make([]string, 201))
	wantKind(t, err, dict.KindBadRequest)
}

func TestLookupEntry(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil { // held by psp
		t.Fatal(err)
	}
	key := phoneEntry().Key

	t.Run("another participant can query", func(t *testing.T) {
		got, err := svc.LookupEntry(ctx, otherPSP, key)
		if err != nil || got.Owner.Name != "João Silva" {
			t.Fatalf("LookupEntry = %+v, %v", got, err)
		}
	})

	t.Run("the holder cannot query its own entry", func(t *testing.T) {
		_, err := svc.LookupEntry(ctx, psp, key)
		wantKind(t, err, dict.KindBookTransfer)
	})

	t.Run("unknown key is not found for anyone", func(t *testing.T) {
		_, err := svc.LookupEntry(ctx, psp, "mail@mail.com")
		wantKind(t, err, dict.KindNotFound)
	})

	t.Run("malformed key", func(t *testing.T) {
		_, err := svc.LookupEntry(ctx, otherPSP, "not-a-key")
		wantKind(t, err, dict.KindBadRequest)
	})
}
