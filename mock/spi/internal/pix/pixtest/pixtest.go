// Package pixtest holds the behavior every pix.PaymentStore must have, so the in-memory and
// the Postgres implementations are tested by the same cases.
package pixtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/spi"
)

func payment(e2e string) pix.Payment {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	return pix.Payment{
		ID: e2e, Kind: pix.KindPayment, EndToEndID: e2e, Status: pix.StatusPending, AmountCents: 12345, Purpose: "IPAY", Initiation: "DICT", Priority: "PAGPRI",
		Info:      "lunch",
		PayerISPB: "99000001", PayerName: "Ana Souza", PayerTaxID: "52998224725", PayerBranch: "0001", PayerAccount: "00123456", PayerAccountType: "CACC",
		PayeeISPB: "99000002", PayeeTaxID: "39053344705", PayeeBranch: "0001", PayeeAccount: "00456789", PayeeAccountType: "CACC", PayeeKey: "39053344705",
		AcceptedAt: at, CreatedAt: at,
	}
}

// norm puts the times in UTC so values read back from a database compare equal.
func norm(p pix.Payment) pix.Payment {
	p.AcceptedAt, p.CreatedAt, p.SettledAt = p.AcceptedAt.UTC(), p.CreatedAt.UTC(), p.SettledAt.UTC()
	return p
}

// Run executes the cases. newStore must return an empty store for each call.
func Run(t *testing.T, newStore func(t *testing.T) pix.PaymentStore) {
	ctx := context.Background()
	const e2e = "E9900000120261114120000000000001"

	t.Run("create and get", func(t *testing.T) {
		s := newStore(t)
		want := payment(e2e)
		got, created, err := s.CreatePayment(ctx, want)
		if err != nil || !created || got.EndToEndID != e2e {
			t.Fatalf("create = %+v, %v, %v", got, created, err)
		}
		read, err := s.GetPayment(ctx, e2e)
		if err != nil {
			t.Fatal(err)
		}
		if norm(read) != norm(want) {
			t.Fatalf("stored = %+v\nwant     %+v", read, want)
		}
	})

	t.Run("create twice keeps the first", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		other := payment(e2e)
		other.AmountCents = 1
		existing, created, err := s.CreatePayment(ctx, other)
		if err != nil || created || existing.AmountCents != 12345 {
			t.Fatalf("second create = %+v, %v, %v; want the existing payment and created == false", existing, created, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetPayment(ctx, e2e); !errors.Is(err, pix.ErrPaymentNotFound) {
			t.Fatalf("get = %v", err)
		}
		if _, _, err := s.FinalizePayment(ctx, e2e, pix.StatusSettled, "", "", time.Now()); !errors.Is(err, pix.ErrPaymentNotFound) {
			t.Fatalf("finalize = %v", err)
		}
	})

	t.Run("settle once", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		at := time.Date(2026, 11, 14, 12, 0, 5, 0, time.UTC)
		p, changed, err := s.FinalizePayment(ctx, e2e, pix.StatusSettled, "", "", at)
		if err != nil || !changed || p.Status != pix.StatusSettled || !p.SettledAt.Equal(at) {
			t.Fatalf("settle = %+v, %v, %v", p, changed, err)
		}
		p, changed, err = s.FinalizePayment(ctx, e2e, pix.StatusRejected, "DS04", "late", at)
		if err != nil || changed || p.Status != pix.StatusSettled {
			t.Fatalf("second finalize = %+v, %v, %v; want unchanged and still settled", p, changed, err)
		}
	})

	t.Run("pending before a time", func(t *testing.T) {
		s := newStore(t)
		old, recent, done := payment("E9900000120261114120000000000001"), payment("E9900000120261114120000000000002"), payment("E9900000120261114120000000000003")
		old.CreatedAt = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		recent.CreatedAt = old.CreatedAt.Add(time.Minute)
		done.CreatedAt = old.CreatedAt
		for _, p := range []pix.Payment{old, recent, done} {
			_, _, _ = s.CreatePayment(ctx, p)
		}
		_, _, _ = s.FinalizePayment(ctx, done.ID, pix.StatusSettled, "", "", old.CreatedAt)

		ids, err := s.PendingBefore(ctx, old.CreatedAt.Add(30*time.Second))
		if err != nil || len(ids) != 1 || ids[0] != old.ID {
			t.Fatalf("pending before = %v, %v; want only the old pending payment", ids, err)
		}
		ids, _ = s.PendingBefore(ctx, old.CreatedAt.Add(time.Hour))
		if len(ids) != 2 {
			t.Fatalf("pending before an hour later = %v; want both pending payments", ids)
		}
	})

	t.Run("returns of a payment", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		ret := func(id, of string, status string) pix.Payment {
			r := payment(of)
			r.ID, r.Kind, r.ReturnReason, r.Status = id, pix.KindReturn, "MD06", status
			return r
		}
		_, _, _ = s.CreatePayment(ctx, ret("D9900000220261114120000000000001", e2e, pix.StatusPending))
		_, _, _ = s.CreatePayment(ctx, ret("D9900000220261114120000000000002", e2e, pix.StatusPending))
		_, _, _ = s.CreatePayment(ctx, ret("D9900000220261114120000000000003", "E9900000120261114120000000000009", pix.StatusPending)) // another payment

		got, err := s.ReturnsOf(ctx, e2e)
		if err != nil || len(got) != 2 {
			t.Fatalf("returns of %s = %d, %v; want the 2 returns of that payment, not the payment itself or others", e2e, len(got), err)
		}
		for _, r := range got {
			if r.Kind != pix.KindReturn || r.EndToEndID != e2e || r.ReturnReason != "MD06" {
				t.Fatalf("return = %+v", r)
			}
		}
		if none, _ := s.ReturnsOf(ctx, "E9900000120261114120000000000007"); len(none) != 0 {
			t.Fatalf("returns of an unknown payment = %d", len(none))
		}
	})

	t.Run("reject keeps the reason", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		p, changed, err := s.FinalizePayment(ctx, e2e, pix.StatusRejected, "AC03", "no such account", time.Now())
		if err != nil || !changed || p.Status != pix.StatusRejected || p.ReasonCode != "AC03" || p.ReasonDetail != "no such account" || !p.SettledAt.IsZero() {
			t.Fatalf("reject = %+v, %v, %v", p, changed, err)
		}
		read, _ := s.GetPayment(ctx, e2e)
		if read.Status != pix.StatusRejected || read.ReasonCode != "AC03" {
			t.Fatalf("stored = %+v", read)
		}
	})
}

func account(number string) pix.Account {
	return pix.Account{
		ISPB: "99000002", Branch: "0001", Number: number, Type: "CACC", HolderTaxID: "39053344705", HolderName: "Carlos Mendes",
		Status: pix.AccountActive, Scenario: pix.ScenarioNormal,
	}
}

// RunAccounts executes the account cases. newStore must return an empty store for each call.
func RunAccounts(t *testing.T, newStore func(t *testing.T) pix.AccountStore) {
	ctx := context.Background()

	t.Run("create and get", func(t *testing.T) {
		s := newStore(t)
		want := account("00456789")
		created, err := s.CreateAccount(ctx, want)
		if err != nil || !created {
			t.Fatalf("create = %v, %v", created, err)
		}
		got, err := s.GetAccount(ctx, "99000002", "00456789")
		if err != nil || got != want {
			t.Fatalf("get = %+v, %v; want %+v", got, err, want)
		}
	})

	t.Run("create twice keeps the first", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.CreateAccount(ctx, account("00456789"))
		other := account("00456789")
		other.Status = pix.AccountClosed
		if created, err := s.CreateAccount(ctx, other); err != nil || created {
			t.Fatalf("second create = %v, %v; want created == false", created, err)
		}
		if got, _ := s.GetAccount(ctx, "99000002", "00456789"); got.Status != pix.AccountActive {
			t.Fatalf("status = %s; the existing account must not change", got.Status)
		}
	})

	t.Run("the number is per participant", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.CreateAccount(ctx, account("00456789"))
		other := account("00456789")
		other.ISPB = "99000003"
		if created, err := s.CreateAccount(ctx, other); err != nil || !created {
			t.Fatalf("same number at another participant = %v, %v; want created", created, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.CreateAccount(ctx, account("00456789"))
		if _, err := s.GetAccount(ctx, "99000002", "99999999"); !errors.Is(err, pix.ErrAccountNotFound) {
			t.Fatalf("unknown number = %v", err)
		}
		if _, err := s.GetAccount(ctx, "99000003", "00456789"); !errors.Is(err, pix.ErrAccountNotFound) {
			t.Fatalf("another participant = %v", err)
		}
	})
}

// RunReports executes the reported transaction cases. newStore must return an empty store for each call.
func RunReports(t *testing.T, newStore func(t *testing.T) pix.ReportStore) {
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	report := func(id string) pix.ReportedTransaction {
		return pix.ReportedTransaction{
			ID: id, EndToEndID: "E9900000120261114120000000000001", Scenario: "BOK1", Initiation: "DICT", AmountCents: 2590, SettledAt: at,
			PayerISPB: "99000001", PayerTaxID: "52998224725", PayerBranch: "0001", PayerAccount: "00123456", PayerAccountType: "CACC",
			PayeeISPB: "99000001", PayeeTaxID: "11144477735", PayeeBranch: "0001", PayeeAccount: "00234567", PayeeAccountType: "SVGS", PayeeKey: "11144477735",
			ReportedBy: "99000001", ReportedAt: at.Add(time.Minute),
		}
	}
	norm := func(r pix.ReportedTransaction) pix.ReportedTransaction {
		r.SettledAt, r.ReportedAt = r.SettledAt.UTC(), r.ReportedAt.UTC()
		return r
	}

	t.Run("add and get", func(t *testing.T) {
		s := newStore(t)
		want := report("E9900000120261114120000000000001")
		added, err := s.AddReported(ctx, want)
		if err != nil || !added {
			t.Fatalf("add = %v, %v", added, err)
		}
		got, err := s.GetReported(ctx, want.ID)
		if err != nil || norm(got) != norm(want) {
			t.Fatalf("get = %+v, %v\nwant %+v", got, err, want)
		}
	})

	t.Run("adding twice keeps the first", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.AddReported(ctx, report("E9900000120261114120000000000001"))
		other := report("E9900000120261114120000000000001")
		other.AmountCents = 1
		if added, err := s.AddReported(ctx, other); err != nil || added {
			t.Fatalf("second add = %v, %v; want added == false", added, err)
		}
		if got, _ := s.GetReported(ctx, other.ID); got.AmountCents != 2590 {
			t.Fatalf("amount = %d; the first report must stay", got.AmountCents)
		}
	})

	t.Run("a return and its payment are reported separately", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.AddReported(ctx, report("E9900000120261114120000000000001"))
		ret := report("D9900000120261114120000000000001")
		if added, err := s.AddReported(ctx, ret); err != nil || !added {
			t.Fatalf("add return = %v, %v", added, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetReported(ctx, "E9900000120261114120000000000009"); !errors.Is(err, pix.ErrReportedNotFound) {
			t.Fatalf("get = %v", err)
		}
	})
}

// Backend is everything the control API reads and clears.
type Backend interface {
	spi.Store
	pix.Storage
	pix.AccountStore
	pix.Inspector
}

// RunInspector executes the inspection cases. newStore must return an empty store for each call.
func RunInspector(t *testing.T, newStore func(t *testing.T) Backend) {
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	t.Run("list payments newest first, filtered and limited", func(t *testing.T) {
		s := newStore(t)
		mk := func(id, kind, status, payer, payee string, minutes int) pix.Payment {
			p := payment(id)
			p.ID, p.Kind, p.Status, p.PayerISPB, p.PayeeISPB = id, kind, status, payer, payee
			p.CreatedAt = at.Add(time.Duration(minutes) * time.Minute)
			return p
		}
		for _, p := range []pix.Payment{
			mk("E9900000120261114120000000000001", pix.KindPayment, pix.StatusSettled, "99000001", "99000002", 1),
			mk("E9900000120261114120000000000002", pix.KindPayment, pix.StatusPending, "99000001", "99000003", 2),
			mk("D9900000320261114120000000000003", pix.KindReturn, pix.StatusSettled, "99000003", "99000001", 3),
		} {
			if _, _, err := s.CreatePayment(ctx, p); err != nil {
				t.Fatal(err)
			}
		}
		ids := func(f pix.PaymentFilter) []string {
			ps, err := s.ListPayments(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			out := make([]string, len(ps))
			for i, p := range ps {
				out[i] = p.ID[len(p.ID)-1:]
			}
			return out
		}
		check := func(name string, got []string, want ...string) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s = %v, want %v", name, got, want)
				}
			}
		}
		check("all", ids(pix.PaymentFilter{}), "3", "2", "1")
		check("settled", ids(pix.PaymentFilter{Status: pix.StatusSettled}), "3", "1")
		check("returns", ids(pix.PaymentFilter{Kind: pix.KindReturn}), "3")
		check("participant 99000003 (payer or payee)", ids(pix.PaymentFilter{Participant: "99000003"}), "3", "2")
		check("limit", ids(pix.PaymentFilter{Limit: 2}), "3", "2")
	})

	t.Run("list messages newest first with the body", func(t *testing.T) {
		s := newStore(t)
		_ = s.AddInbound(ctx, []spi.Message{{ResourceID: "in1", ISPB: "11111111", Body: []byte("<a/>"), CreatedAt: at}})
		_ = s.AddOutbound(ctx, spi.Message{ResourceID: "out1", ISPB: "11111111", Body: []byte("<b/>"), CreatedAt: at})
		_ = s.AddOutbound(ctx, spi.Message{ResourceID: "out2", ISPB: "22222222", Body: []byte("<c/>"), CreatedAt: at})

		all, err := s.ListMessages(ctx, pix.MessageFilter{})
		if err != nil || len(all) != 3 || all[0].ResourceID != "out2" || all[2].ResourceID != "in1" || all[2].Body != "<a/>" || all[2].Direction != "in" {
			t.Fatalf("all = %+v, %v", all, err)
		}
		mine, _ := s.ListMessages(ctx, pix.MessageFilter{Participant: "11111111"})
		if len(mine) != 2 {
			t.Fatalf("participant filter = %d", len(mine))
		}
		out, _ := s.ListMessages(ctx, pix.MessageFilter{Direction: "out", Limit: 1})
		if len(out) != 1 || out[0].ResourceID != "out2" || out[0].Direction != "out" {
			t.Fatalf("direction and limit = %+v", out)
		}
	})

	t.Run("list accounts ordered", func(t *testing.T) {
		s := newStore(t)
		for _, a := range []pix.Account{account("00000002"), account("00000001")} {
			_, _ = s.CreateAccount(ctx, a)
		}
		other := account("00000000")
		other.ISPB = "99000003"
		_, _ = s.CreateAccount(ctx, other)
		got, err := s.ListAccounts(ctx)
		if err != nil || len(got) != 3 || got[0].Number != "00000001" || got[1].Number != "00000002" || got[2].ISPB != "99000003" {
			t.Fatalf("accounts = %+v, %v", got, err)
		}
	})

	t.Run("reset clears the state and keeps the accounts and the message sequence", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.CreateAccount(ctx, account("00456789"))
		_, _, _ = s.CreatePayment(ctx, payment("E9900000120261114120000000000001"))
		_, _ = s.AddReported(ctx, pix.ReportedTransaction{ID: "E9900000120261114120000000000002", EndToEndID: "E9900000120261114120000000000002",
			Scenario: "BOK1", Initiation: "MANU", AmountCents: 1, SettledAt: at, PayerISPB: "99000001", PayeeISPB: "99000001",
			PayerAccount: "1", PayeeAccount: "2", PayerAccountType: "CACC", PayeeAccountType: "CACC", ReportedBy: "99000001", ReportedAt: at})
		_ = s.AddOutbound(ctx, spi.Message{ResourceID: "o1", ISPB: "11111111", Body: []byte("x"), CreatedAt: at})
		before, _ := s.ReadAfter(ctx, "11111111", 0, 1)
		_ = s.Ack(ctx, "11111111", before[0].Seq)

		if err := s.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		if ps, _ := s.ListPayments(ctx, pix.PaymentFilter{}); len(ps) != 0 {
			t.Fatalf("payments left: %d", len(ps))
		}
		if ms, _ := s.ListMessages(ctx, pix.MessageFilter{}); len(ms) != 0 {
			t.Fatalf("messages left: %d", len(ms))
		}
		if _, err := s.GetReported(ctx, "E9900000120261114120000000000002"); !errors.Is(err, pix.ErrReportedNotFound) {
			t.Fatalf("reported transaction left: %v", err)
		}
		if acked, _ := s.Acked(ctx, "11111111"); acked != 0 {
			t.Fatalf("acked = %d after the reset", acked)
		}
		if as, _ := s.ListAccounts(ctx); len(as) != 1 {
			t.Fatalf("accounts = %d; a reset keeps them", len(as))
		}
		_ = s.AddOutbound(ctx, spi.Message{ResourceID: "o2", ISPB: "11111111", Body: []byte("y"), CreatedAt: at})
		after, _ := s.ReadAfter(ctx, "11111111", 0, 1)
		if len(after) != 1 || after[0].Seq <= before[0].Seq {
			t.Fatalf("sequence went back: %d after %d", after[0].Seq, before[0].Seq)
		}
	})
}

// NotificationBackend is a store with payments and the DICT feed's outbox.
type NotificationBackend interface {
	pix.PaymentStore
	pix.NotificationStore
}

// RunNotifications executes the cases of the DICT feed's outbox.
func RunNotifications(t *testing.T, newStore func(t *testing.T) NotificationBackend) {
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	settle := func(t *testing.T, s NotificationBackend, p pix.Payment, settledAt time.Time) {
		t.Helper()
		if _, _, err := s.CreatePayment(ctx, p); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.FinalizePayment(ctx, p.ID, pix.StatusSettled, "", "", settledAt); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(ps []pix.Payment) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.ID
		}
		return out
	}

	t.Run("settled payments, oldest first", func(t *testing.T) {
		s := newStore(t)
		settle(t, s, payment("E9900000120261114120000000000002"), at.Add(time.Minute))
		settle(t, s, payment("E9900000120261114120000000000001"), at)
		pending := payment("E9900000120261114120000000000003")
		_, _, _ = s.CreatePayment(ctx, pending)
		rejected := payment("E9900000120261114120000000000004")
		_, _, _ = s.CreatePayment(ctx, rejected)
		_, _, _ = s.FinalizePayment(ctx, rejected.ID, pix.StatusRejected, "AC03", "", at)
		ret := payment("D99000002202611141200RETURN00001")
		ret.Kind, ret.EndToEndID = pix.KindReturn, "E9900000120261114120000000000001"
		settle(t, s, ret, at)

		got, err := s.PendingNotifications(ctx, at.Add(-time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"E9900000120261114120000000000001", "E9900000120261114120000000000002"}; !slices.Equal(ids(got), want) {
			t.Fatalf("pending = %v, want %v", ids(got), want)
		}
		if got[0].PayeeKey != "39053344705" || got[0].SettledAt.IsZero() {
			t.Fatalf("pending payment = %+v", got[0])
		}
		if got, _ := s.PendingNotifications(ctx, at.Add(-time.Hour), 1); len(got) != 1 {
			t.Fatalf("limit 1 gave %d", len(got))
		}
		if got, _ := s.PendingNotifications(ctx, at.Add(time.Second), 10); !slices.Equal(ids(got), []string{"E9900000120261114120000000000002"}) {
			t.Fatalf("since a second later = %v", ids(got))
		}
	})

	t.Run("marked ones leave the outbox", func(t *testing.T) {
		s := newStore(t)
		settle(t, s, payment("E9900000120261114120000000000001"), at)
		settle(t, s, payment("E9900000120261114120000000000002"), at)
		if err := s.MarkNotified(ctx, "E9900000120261114120000000000001", at); err != nil {
			t.Fatal(err)
		}
		got, err := s.PendingNotifications(ctx, at.Add(-time.Hour), 10)
		if err != nil || !slices.Equal(ids(got), []string{"E9900000120261114120000000000002"}) {
			t.Fatalf("pending = %v, %v", ids(got), err)
		}
	})
}
