package pix_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/seed"
	"pixfake/mock/spi/internal/spi"
)

// newRulesEnv starts the SPI the way the binary does: the seeded accounts and the account rules.
func newRulesEnv(t *testing.T) *env {
	t.Helper()
	e := newEnvWith(t, func(s *memstore.Store) pix.Responder { return pix.AccountResponder{Accounts: s} })
	if _, err := pix.SeedAccounts(context.Background(), e.store, seed.Accounts()); err != nil {
		t.Fatal(err)
	}
	return e
}

// toAccount makes the wallet pay an account at a simulated bank, with the holder's CPF.
func toAccount(ispb, number, taxID string) order {
	return order{payee: ispb, mutateTx: func(tx *iso.CreditTransfer) {
		tx.CreditorAcct.ID.Othr.Number = number
		tx.Creditor.ID.PrvtID.Othr.ID = taxID
	}}
}

func TestAccountRules(t *testing.T) {
	tests := []struct {
		name   string
		o      order
		reason string // empty: settled
	}{
		{"active account at Banco Alfa", toAccount(alfa, "00456789", "39053344705"), ""},
		{"active account at Banco Beta", toAccount(beta, "00567890", "16899535009"), ""},
		{"closed account", toAccount(alfa, seed.ClosedAccount, "24681357928"), "AC07"},
		{"blocked account", toAccount(alfa, seed.BlockedAccount, "13579246828"), "AC06"},
		{"account that does not exist", toAccount(alfa, "99999999", "39053344705"), "AC03"},
		{"account of another participant", toAccount(beta, "00456789", "39053344705"), "AC03"},
		{"other branch", order{payee: alfa, mutateTx: func(tx *iso.CreditTransfer) { tx.CreditorAcct.ID.Othr.Branch = "0002" }}, "AC03"},
		{"another CPF than the holder", toAccount(alfa, "00456789", "16899535009"), "BE01"},
		{"closed wins over the CPF mismatch", toAccount(alfa, seed.ClosedAccount, "16899535009"), "AC07"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRulesEnv(t)
			e.sendOrder(tt.o)
			sts := e.readStatuses(wallet)
			if len(sts) != 1 {
				t.Fatalf("statuses = %+v", sts)
			}
			p := e.payment(e2eA)
			if tt.reason == "" {
				if sts[0].Status != iso.StatusSettledPayer || p.Status != pix.StatusSettled {
					t.Fatalf("status %s / payment %s; want settled", sts[0].Status, p.Status)
				}
				return
			}
			if sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != tt.reason || p.Status != pix.StatusRejected || p.ReasonCode != tt.reason {
				t.Fatalf("statuses %+v / payment %+v; want RJCT %s", sts, p, tt.reason)
			}
		})
	}
}

func TestSilentAccountTimesOut(t *testing.T) {
	e := newRulesEnv(t)
	e.sendOrder(toAccount(beta, seed.SilentAccount, "86420975310"))

	if p := e.payment(e2eA); p.Status != pix.StatusPending {
		t.Fatalf("status = %s; the bank said nothing, so the payment must wait", p.Status)
	}
	if got := e.readStatuses(wallet); len(got) != 0 {
		t.Fatalf("payer got %+v before the timeout", got)
	}

	e.advance(pix.DefaultTimeout - time.Second)
	if n := e.proc.ExpirePending(context.Background()); n != 0 {
		t.Fatalf("expired %d payments just before the limit", n)
	}

	e.advance(2 * time.Second)
	if n := e.proc.ExpirePending(context.Background()); n != 1 {
		t.Fatalf("expired %d payments after the limit, want 1", n)
	}
	p := e.payment(e2eA)
	if p.Status != pix.StatusRejected || p.ReasonCode != "AB03" {
		t.Fatalf("payment = %+v; want rejected with AB03", p)
	}
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AB03" {
		t.Fatalf("statuses = %+v; want RJCT AB03", sts)
	}

	if n := e.proc.ExpirePending(context.Background()); n != 0 {
		t.Fatalf("expired %d payments on the second run", n)
	}
	if got := e.readStatuses(wallet); len(got) != 0 {
		t.Fatalf("second run sent %+v", got)
	}
}

func TestWalletThatDoesNotAnswerTimesOutToo(t *testing.T) {
	e := newRulesEnv(t)
	e.proc.Timeout = 5 * time.Second // shortened, as a test would configure it
	e.sendOrder(order{sender: alfa, payee: wallet})
	e.read(wallet)

	e.advance(6 * time.Second)
	e.proc.ExpirePending(context.Background())
	sts := e.readStatuses(alfa)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AB03" {
		t.Fatalf("payer statuses = %+v; want RJCT AB03", sts)
	}

	// The answer comes too late: it changes nothing.
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))
	if p := e.payment(e2eA); p.Status != pix.StatusRejected {
		t.Fatalf("status = %s; a payment rejected by timeout cannot settle", p.Status)
	}
	if n := len(e.read(wallet)) + len(e.read(alfa)); n != 0 {
		t.Fatalf("%d messages sent after the timeout", n)
	}
}

func TestAnswerBeforeTheTimeoutWins(t *testing.T) {
	e := newRulesEnv(t)
	e.sendOrder(order{sender: alfa, payee: wallet})
	e.read(wallet)
	e.advance(10 * time.Second)
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))

	e.advance(time.Minute)
	if n := e.proc.ExpirePending(context.Background()); n != 0 {
		t.Fatalf("expired %d payments; the settled one is no longer pending", n)
	}
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("status = %s", p.Status)
	}
}

// The binary runs ExpirePending on a timer; this checks that loop with the real clock.
func TestRunExpiryRejectsInTheBackground(t *testing.T) {
	store := memstore.New()
	if _, err := pix.SeedAccounts(context.Background(), store, seed.Accounts()); err != nil {
		t.Fatal(err)
	}
	svc := spi.NewService(store, 0)
	proc := pix.NewProcessor(svc, store, pix.DefaultParticipants(), pix.AccountResponder{Accounts: store}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	proc.Timeout = 50 * time.Millisecond
	svc.SetHandler(proc)
	e := &env{t: t, store: store, svc: svc, proc: proc}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go proc.RunExpiry(ctx, 10*time.Millisecond)

	id := "E99000001" + time.Now().UTC().Format("200601021504") + "RUNEXPIRY01"
	o := toAccount(beta, seed.SilentAccount, "86420975310")
	o.e2e = id
	e.sendOrder(o)

	deadline := time.Now().Add(5 * time.Second)
	for e.payment(id).Status == pix.StatusPending {
		if time.Now().After(deadline) {
			t.Fatal("the payment was not rejected in the background")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p := e.payment(id); p.ReasonCode != "AB03" {
		t.Fatalf("payment = %+v; want AB03", p)
	}
	if sts := e.readStatuses(wallet); len(sts) != 1 || sts[0].ReasonCode() != "AB03" {
		t.Fatalf("statuses = %+v", sts)
	}
}

func TestAccountResponderReportsStoreProblems(t *testing.T) {
	d := pix.AccountResponder{Accounts: brokenAccounts{}}.Decide(context.Background(), pix.Payment{PayeeISPB: alfa, PayeeAccount: "1"})
	if d.Accept || d.NoAnswer || d.ReasonCode != "ED05" {
		t.Fatalf("decision = %+v; want a rejection with ED05", d)
	}
}

type brokenAccounts struct{}

func (brokenAccounts) CreateAccount(context.Context, pix.Account) (bool, error) {
	return false, errors.New("down")
}
func (brokenAccounts) GetAccount(context.Context, string, string) (pix.Account, error) {
	return pix.Account{}, errors.New("down")
}
