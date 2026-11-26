package dict_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
)

const otherRequestID = "b946d533-7f22-42a5-9a9b-e87cd55c0f4e"

// vsync is what a participant computes on its side.
func vsync(t *testing.T, cids ...string) string {
	t.Helper()
	var v dict.VSync
	for _, c := range cids {
		if err := v.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	return v.String()
}

func TestReconciliation(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	phone, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry()))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := dict.CID(phoneEntry(), requestID)
	if phone.CID != want || phone.RequestID != requestID {
		t.Fatalf("cid = %s, request id = %s", phone.CID, phone.RequestID)
	}
	email := phoneEntry()
	email.Key, email.KeyType = "joao@example.com", dict.KeyEmail
	if _, err := svc.CreateEntry(ctx, psp, dict.CreateEntryInput{Entry: email, Reason: dict.ReasonUserRequested, RequestID: otherRequestID}); err != nil {
		t.Fatal(err)
	}

	t.Run("sync verification", func(t *testing.T) {
		ok, err := svc.CreateSyncVerification(ctx, psp, dict.SyncVerification{Participant: psp, KeyType: dict.KeyPhone, Verifier: vsync(t, phone.CID)})
		if err != nil || ok.Result != dict.SyncOK || ok.ID == 0 {
			t.Fatalf("same set: %+v, %v", ok, err)
		}
		nok, err := svc.CreateSyncVerification(ctx, psp, dict.SyncVerification{Participant: psp, KeyType: dict.KeyPhone, Verifier: dict.ZeroVSync})
		if err != nil || nok.Result != dict.SyncNOK || nok.ID == ok.ID {
			t.Fatalf("different set: %+v, %v", nok, err)
		}
		empty, err := svc.CreateSyncVerification(ctx, psp, dict.SyncVerification{Participant: psp, KeyType: dict.KeyCPF, Verifier: dict.ZeroVSync})
		if err != nil || empty.Result != dict.SyncOK {
			t.Fatalf("no CPF keys: %+v, %v", empty, err)
		}
		_, err = svc.CreateSyncVerification(ctx, otherPSP, dict.SyncVerification{Participant: psp, KeyType: dict.KeyPhone, Verifier: dict.ZeroVSync})
		wantKind(t, err, dict.KindForbidden)
		_, err = svc.CreateSyncVerification(ctx, psp, dict.SyncVerification{Participant: psp, KeyType: dict.KeyPhone, Verifier: "abc"})
		wantKind(t, err, dict.KindBadRequest)
	})

	t.Run("CID file", func(t *testing.T) {
		f, err := svc.CreateCIDFile(ctx, psp, psp, dict.KeyPhone)
		if err != nil || f.Status != dict.FileRequested || f.ID == 0 {
			t.Fatalf("create: %+v, %v", f, err)
		}
		got, err := svc.GetCIDFile(ctx, psp, f.ID)
		if err != nil || got.Status != dict.FileAvailable || got.CreationTime.IsZero() {
			t.Fatalf("get: %+v, %v", got, err)
		}
		var lines []string
		for sc := bufio.NewScanner(bytes.NewReader(got.Content)); sc.Scan(); {
			lines = append(lines, sc.Text())
		}
		sum := sha256.Sum256(got.Content)
		if len(lines) != 1 || lines[0] != phone.CID || got.Sha256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("content %q, sha %s", got.Content, got.Sha256)
		}
		if content, err := svc.CIDFileContent(ctx, f.ID); err != nil || !bytes.Equal(content, got.Content) {
			t.Fatalf("download: %q, %v", content, err)
		}
		_, err = svc.GetCIDFile(ctx, otherPSP, f.ID)
		wantKind(t, err, dict.KindForbidden)
		_, err = svc.GetCIDFile(ctx, psp, 999)
		wantKind(t, err, dict.KindNotFound)
		_, err = svc.CreateCIDFile(ctx, psp, otherPSP, dict.KeyPhone)
		wantKind(t, err, dict.KindForbidden)
	})

	t.Run("entry by CID", func(t *testing.T) {
		e, err := svc.EntryByCID(ctx, psp, phone.CID)
		if err != nil || e.Key != phone.Key || e.RequestID != requestID {
			t.Fatalf("%+v, %v", e, err)
		}
		_, err = svc.EntryByCID(ctx, otherPSP, phone.CID)
		wantKind(t, err, dict.KindForbidden)
		_, err = svc.EntryByCID(ctx, psp, dict.ZeroVSync)
		wantKind(t, err, dict.KindNotFound)
		_, err = svc.EntryByCID(ctx, psp, "not-a-cid")
		wantKind(t, err, dict.KindNotFound)
	})
}

func TestReconciliationFollowsChanges(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	created, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry()))
	if err != nil {
		t.Fatal(err)
	}

	owner := phoneEntry().Owner
	owner.Name = "João da Silva"
	updated, err := svc.UpdateEntry(ctx, psp, created.Key, dict.UpdateEntryInput{Key: created.Key, Owner: &owner, Reason: dict.ReasonUserRequested})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CID == created.CID || updated.RequestID != requestID {
		t.Fatalf("an update must change the CID and keep the RequestId: %+v", updated)
	}
	if v, _ := svc.CreateSyncVerification(ctx, psp, dict.SyncVerification{Participant: psp, KeyType: dict.KeyPhone, Verifier: vsync(t, updated.CID)}); v.Result != dict.SyncOK {
		t.Fatalf("after the update: %+v", v)
	}
	if _, err := svc.EntryByCID(ctx, psp, created.CID); err == nil {
		t.Fatal("the old CID still finds the entry")
	}

	if err := svc.DeleteEntry(ctx, psp, created.Key, dict.DeleteEntryInput{Key: created.Key, Participant: psp, Reason: dict.ReasonUserRequested}); err != nil {
		t.Fatal(err)
	}

	page, err := svc.ListCIDEvents(ctx, psp, dict.ListCIDEventsInput{Participant: psp, KeyType: dict.KeyPhone})
	if err != nil {
		t.Fatal(err)
	}
	types := ""
	for _, ev := range page.Events {
		types += string(ev.Type)[:1]
	}
	if types != "ARAR" {
		t.Fatalf("events %s, want added, removed and added (update), removed", types)
	}
	if page.Events[0].CID != created.CID || page.Events[2].CID != updated.CID || page.HasMore {
		t.Fatalf("page %+v", page)
	}
	if page.VerifierStart != created.CID || page.VerifierEnd != dict.ZeroVSync {
		t.Fatalf("verifiers %s .. %s", page.VerifierStart, page.VerifierEnd)
	}

	two, err := svc.ListCIDEvents(ctx, psp, dict.ListCIDEventsInput{Participant: psp, KeyType: dict.KeyPhone, Limit: 2})
	if err != nil || len(two.Events) != 2 || !two.HasMore || two.VerifierEnd != dict.ZeroVSync {
		t.Fatalf("limit 2: %+v, %v", two, err)
	}

	later, err := svc.ListCIDEvents(ctx, psp, dict.ListCIDEventsInput{Participant: psp, KeyType: dict.KeyPhone, Start: time.Now().Add(time.Hour)})
	if err != nil || len(later.Events) != 0 || later.VerifierStart != dict.ZeroVSync || later.VerifierEnd != dict.ZeroVSync {
		t.Fatalf("after every event: %+v, %v", later, err)
	}

	_, err = svc.ListCIDEvents(ctx, psp, dict.ListCIDEventsInput{Participant: psp, KeyType: dict.KeyPhone, Limit: 201})
	wantKind(t, err, dict.KindBadRequest)
	_, err = svc.ListCIDEvents(ctx, otherPSP, dict.ListCIDEventsInput{Participant: psp, KeyType: dict.KeyPhone})
	wantKind(t, err, dict.KindForbidden)
}

// The seed's entries have the CIDs the wallet computes on its side, from the same RequestIds.
func TestSeedCIDs(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	e := phoneEntry()
	if _, err := svc.Seed(ctx, []dict.Entry{e}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetEntry(ctx, e.Key)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := dict.CID(e, dict.SeedRequestID(e.Key))
	if got.CID != want || got.RequestID != dict.SeedRequestID(e.Key) {
		t.Fatalf("cid %s, want %s", got.CID, want)
	}
}
