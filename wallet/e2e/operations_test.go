package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/seed"
)

// run calls one of the wallet's operations commands the way an operator would, on the wallet's database.
func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, code := runExit(t, name, args...)
	if code != 0 {
		t.Fatalf("%s %v: exit %d\n%s", name, args, code, out)
	}
	return out
}

// runExit is run for a command whose exit code is part of the answer.
func runExit(t *testing.T, name string, args ...string) (string, int) {
	t.Helper()
	bin, url := os.Getenv("WALLET_E2E_BIN"), os.Getenv("WALLET_E2E_DATABASE_URL")
	if bin == "" || url == "" {
		t.Skip("WALLET_E2E_BIN and WALLET_E2E_DATABASE_URL are not set; run `make e2e` at the repository root")
	}
	cmd := exec.Command(filepath.Join(bin, "wallet-"+name), args...)
	cmd.Env = append(os.Environ(), "WALLET_DATABASE_URL="+url)
	if dict := os.Getenv("WALLET_E2E_DICT_URL"); dict != "" {
		cmd.Env = append(cmd.Env, "DICT_BASE_URL="+dict)
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out), 0
}

func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("WALLET_E2E_DATABASE_URL")
	if url == "" {
		t.Skip("WALLET_E2E_DATABASE_URL is not set; run `make e2e` at the repository root")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestReceiveRefusedByAccount(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	t.Cleanup(func() {
		run(t, "accounts", "status", seed.AnaID, "ACTIVE")
		run(t, "accounts", "limit", seed.AnaID, "0")
	})

	for _, tt := range []struct {
		name, cmd, value, code string
	}{
		{"blocked", "status", "BLOCKED", "AC06"},
		{"closed", "status", "CLOSED", "AC07"},
		{"above the limit", "limit", "299", "AM02"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			run(t, "accounts", tt.cmd, seed.AnaID, tt.value)
			t.Cleanup(func() {
				run(t, "accounts", "status", seed.AnaID, "ACTIVE")
				run(t, "accounts", "limit", seed.AnaID, "0")
			})
			before := e.balance(t)

			id := e.bankPays(t, `{"from":"99000002","to":"99000001","to_account":"00123456","amount":"3.00"}`)
			p := e.mockPayment(t, id, func(p map[string]any) bool { return p["reason_code"] != nil })
			if p["reason_code"] != tt.code {
				t.Errorf("the SPI says %v, want %s", p["reason_code"], tt.code)
			}
			if e.receivedBy(t, seed.AnaID, id) != nil || e.balance(t).AvailableCents != before.AvailableCents {
				t.Error("a refused payment left a trace")
			}
		})
	}

	run(t, "accounts", "limit", seed.AnaID, "300")
	id := e.bankPays(t, `{"from":"99000002","to":"99000001","to_account":"00123456","amount":"3.00"}`)
	if got := e.waitReceived(t, id); got == nil {
		t.Fatal("exactly the limit must be accepted")
	}
}

func TestPayFromBlockedAccount(t *testing.T) {
	e := setup(t)
	r := e.resolve(t, carlosPhone)
	run(t, "accounts", "status", seed.AnaID, "BLOCKED")
	t.Cleanup(func() { run(t, "accounts", "status", seed.AnaID, "ACTIVE") })
	before := e.balance(t)

	_, err := e.pay(t, r.ResolutionId, 100, "lunch")
	wantCode(t, "pay from a blocked account", err, codes.FailedPrecondition)
	if got := e.balance(t); got.AvailableCents != before.AvailableCents || got.ReservedCents != before.ReservedCents {
		t.Errorf("balance %v -> %v", before, got)
	}
}

func reportOf(t *testing.T, pool *pgxpool.Pool, paymentID string) (string, string) {
	t.Helper()
	var status, reason string
	if err := pool.QueryRow(context.Background(), `SELECT report_status, report_reason FROM payments WHERE id = $1`, paymentID).
		Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	return status, reason
}

func waitReport(t *testing.T, pool *pgxpool.Pool, paymentID, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if status, _ := reportOf(t, pool, paymentID); status == want {
			return
		}
		if time.Now().After(deadline) {
			status, reason := reportOf(t, pool, paymentID)
			t.Fatalf("report of %s is %q (%s), want %q: is the worker running?", paymentID, status, reason, want)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestResendRefusedReport(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	pool := database(t)
	r := e.resolve(t, "+5511999990002")
	p, err := e.pay(t, r.ResolutionId, 120, "to Bruno")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, pool, p.Id, "ACCEPTED")

	// the mock accepts every well-formed report, so the refusal a real SPI could send is written in its place
	if _, err := pool.Exec(context.Background(),
		`UPDATE payments SET report_status = 'REJECTED', report_reason = 'AG01' WHERE id = $1`, p.Id); err != nil {
		t.Fatal(err)
	}
	if out := run(t, "reports", "list"); !strings.Contains(out, p.Id) || !strings.Contains(out, "AG01") ||
		!strings.Contains(out, strconv.Itoa(120)) {
		t.Fatalf("list does not show the refused report:\n%s", out)
	}
	run(t, "reports", "resend", p.Id)
	waitReport(t, pool, p.Id, "ACCEPTED")
	if out := run(t, "reports", "list"); strings.Contains(out, p.Id) {
		t.Fatalf("still listed after it was accepted:\n%s", out)
	}
}

// A key removed from the DICT behind the wallet's back shows up in `keys check`, which fails until the copy is fixed.
func TestKeysCheck(t *testing.T) {
	e := setup(t)
	if out, code := runExit(t, "keys", "check"); code != 0 {
		t.Fatalf("the seed data disagrees with the DICT:\n%s", out)
	}

	key := uniqueEmail(t)
	if _, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, key, requestID(t)); err != nil {
		t.Fatal(err)
	}
	e.cleanup(t, seed.AnaID, key)
	if err := e.wallet.DeleteEntry(ctx(t), key, pix.ReasonUserRequested); err != nil {
		t.Fatal(err)
	}

	out, code := runExit(t, "keys", "check")
	if code != 1 || !strings.Contains(out, key+"\tEMAIL\t"+seed.AnaID) {
		t.Fatalf("exit %d, want 1 and the key listed:\n%s", code, out)
	}

	if err := e.remove(t, seed.AnaID, key); err != nil {
		t.Fatalf("removing a key the DICT no longer has: %v", err)
	}
	if out, code := runExit(t, "keys", "check"); code != 0 {
		t.Fatalf("after removing it: exit %d\n%s", code, out)
	}
}
