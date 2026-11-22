package seed_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/seed"
)

func TestSeed(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	accounts := seed.Accounts()

	res, err := pix.SeedAccounts(ctx, store, accounts)
	if err != nil {
		t.Fatalf("seed data is invalid: %v", err)
	}
	if len(res.Created) != len(accounts) {
		t.Fatalf("created %d of %d accounts", len(res.Created), len(accounts))
	}
	if res, err = pix.SeedAccounts(ctx, store, accounts); err != nil || len(res.Created) != 0 {
		t.Fatalf("second run = %+v, %v; want nothing created", res, err)
	}
	for _, a := range accounts {
		got, err := store.GetAccount(ctx, a.ISPB, a.Number)
		if err != nil || got != a {
			t.Fatalf("GetAccount(%s/%s) = %+v, %v", a.ISPB, a.Number, got, err)
		}
	}
}

// Every account belongs to a participant the SPI knows, and the scenarios of the decision exist.
func TestSeedDataCoverage(t *testing.T) {
	known := pix.DefaultParticipants()
	byNumber := map[string]pix.Account{}
	for _, a := range seed.Accounts() {
		if !known.Known(a.ISPB) {
			t.Errorf("account %s/%s is at participant %s, which the SPI does not know", a.ISPB, a.Number, a.ISPB)
		}
		byNumber[a.Number] = a
	}
	closed, blocked, silent := byNumber[seed.ClosedAccount], byNumber[seed.BlockedAccount], byNumber[seed.SilentAccount]
	if closed.Status != pix.AccountClosed || closed.ISPB != seed.AlfaISPB {
		t.Errorf("closed test account = %+v", closed)
	}
	if blocked.Status != pix.AccountBlocked || blocked.ISPB != seed.AlfaISPB {
		t.Errorf("blocked test account = %+v", blocked)
	}
	if silent.Scenario != pix.ScenarioNoAnswer || silent.ISPB != seed.BetaISPB {
		t.Errorf("silent test account = %+v", silent)
	}
}

// Keeps the docs from falling behind: every seeded account must appear in the README.
func TestSeedAccountsInReadme(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range seed.Accounts() {
		if !strings.Contains(string(readme), a.Number) {
			t.Errorf("account %s is seeded but missing from the README seed table", a.Number)
		}
	}
}

// The SPI and DICT seeds must describe the same accounts: a key found in the DICT has to be
// payable in the SPI. The DICT module cannot be imported, so its seed file is read as text.
func TestSeedMatchesDICT(t *testing.T) {
	src, err := os.ReadFile("../../../dict/internal/seed/data.go")
	if err != nil {
		t.Skip("DICT seed not found next to the SPI mock")
	}
	text := string(src)

	consts := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+)\s*=\s*"([^"]+)"`).FindAllStringSubmatch(text, -1) {
		consts[m[1]] = m[2]
	}
	resolve := func(s string) string {
		if v, ok := consts[s]; ok {
			return v
		}
		return strings.Trim(s, `"`)
	}
	types := map[string]string{"AccountChecking": "CACC", "AccountSavings": "SVGS", "AccountTransact": "TRAN"}

	type holder struct{ ispb, number, typ, taxID, name string }
	var dict []holder
	for _, m := range regexp.MustCompile(`(?:natural|legal)\((\w+), ([\w"]+), dict\.(\w+), "(\d+)", "([^"]+)"`).FindAllStringSubmatch(text, -1) {
		dict = append(dict, holder{resolve(m[1]), resolve(m[2]), types[m[3]], m[4], m[5]})
	}
	if len(dict) == 0 {
		t.Fatal("found no accounts in the DICT seed; the file format changed and this test needs an update")
	}

	spi := map[holder]bool{}
	for _, a := range seed.Accounts() {
		spi[holder{a.ISPB, a.Number, a.Type, a.HolderTaxID, a.HolderName}] = true
	}
	for _, h := range dict {
		if !spi[h] {
			t.Errorf("account %+v is in the DICT seed but not in the SPI seed (or differs)", h)
		}
		delete(spi, h)
	}
	for h := range spi {
		t.Errorf("account %+v is in the SPI seed but not in the DICT seed", h)
	}
}
