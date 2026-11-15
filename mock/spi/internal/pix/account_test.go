package pix_test

import (
	"context"
	"strings"
	"testing"

	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
)

func goodAccount(number string) pix.Account {
	return pix.Account{
		ISPB: alfa, Branch: "0001", Number: number, Type: "CACC", HolderTaxID: "39053344705", HolderName: "Carlos Mendes",
		Status: pix.AccountActive, Scenario: pix.ScenarioNormal,
	}
}

func TestSeedAccountsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	accounts := []pix.Account{goodAccount("00000001"), goodAccount("00000002")}

	res, err := pix.SeedAccounts(ctx, store, accounts)
	if err != nil || len(res.Created) != 2 || len(res.Existing) != 0 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	res, err = pix.SeedAccounts(ctx, store, accounts)
	if err != nil || len(res.Created) != 0 || len(res.Existing) != 2 {
		t.Fatalf("second run = %+v, %v; want everything already existing", res, err)
	}
}

func TestSeedAccountsRejectsBadDataBeforeWritingAnything(t *testing.T) {
	bad := map[string]func(*pix.Account){
		"ISPB":        func(a *pix.Account) { a.ISPB = "12" },
		"number":      func(a *pix.Account) { a.Number = "" },
		"branch":      func(a *pix.Account) { a.Branch = "ABCD" },
		"type":        func(a *pix.Account) { a.Type = "SLRY" },
		"tax id":      func(a *pix.Account) { a.HolderTaxID = "123" },
		"holder name": func(a *pix.Account) { a.HolderName = "" },
		"status":      func(a *pix.Account) { a.Status = "GONE" },
		"scenario":    func(a *pix.Account) { a.Scenario = "SLOW" },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			store := memstore.New()
			broken := goodAccount("00000002")
			mutate(&broken)
			_, err := pix.SeedAccounts(context.Background(), store, []pix.Account{goodAccount("00000001"), broken})
			if err == nil {
				t.Fatal("expected an error")
			}
			if _, err := store.GetAccount(context.Background(), alfa, "00000001"); err == nil {
				t.Fatal("the valid account was written although another one is invalid")
			}
		})
	}
}

func TestSeedAccountsRejectsDuplicates(t *testing.T) {
	_, err := pix.SeedAccounts(context.Background(), memstore.New(), []pix.Account{goodAccount("00000001"), goodAccount("00000001")})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want a duplicate error", err)
	}
}
