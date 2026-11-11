package seed_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/memstore"
	"pixfake/mock/dict/internal/seed"
)

func TestSeedDataIsValidAndIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := dict.NewService(memstore.New())
	entries := seed.Entries()

	res, err := svc.Seed(ctx, entries)
	if err != nil {
		t.Fatalf("seed data is invalid: %v", err)
	}
	if len(res.Created) != len(entries) {
		t.Fatalf("created %d of %d entries", len(res.Created), len(entries))
	}
	if res, err = svc.Seed(ctx, entries); err != nil || len(res.Created) != 0 || len(res.Existing) != len(entries) {
		t.Fatalf("second run = %+v, %v; want everything already existing", res, err)
	}

	for _, e := range entries {
		got, err := svc.GetEntry(ctx, e.Key)
		if err != nil || got.Account.Participant != e.Account.Participant || got.Owner.Name != e.Owner.Name {
			t.Fatalf("GetEntry(%q) = %+v, %v", e.Key, got, err)
		}
	}
}

// The seed must support every test scenario: all key types, natural and legal
// persons, and payments between different institutions.
func TestSeedDataCoverage(t *testing.T) {
	keyTypes := map[dict.KeyType]bool{}
	people := map[dict.PersonType]bool{}
	participants := map[string]bool{}
	for _, e := range seed.Entries() {
		keyTypes[e.KeyType] = true
		people[e.Owner.Type] = true
		participants[e.Account.Participant] = true
	}
	for _, kt := range []dict.KeyType{dict.KeyCPF, dict.KeyCNPJ, dict.KeyPhone, dict.KeyEmail, dict.KeyEVP} {
		if !keyTypes[kt] {
			t.Errorf("no seeded key of type %s", kt)
		}
	}
	if !people[dict.NaturalPerson] || !people[dict.LegalPerson] {
		t.Errorf("seed must have natural and legal persons, got %v", people)
	}
	if len(participants) < 3 {
		t.Errorf("seed must span at least 3 participants, got %v", participants)
	}
	if !participants[seed.WalletISPB] {
		t.Errorf("no entries for the wallet (%s)", seed.WalletISPB)
	}
}

// Keeps the docs from falling behind: every seeded key must appear in the README.
func TestEveryKeyIsDocumentedInReadme(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range seed.Entries() {
		if !strings.Contains(string(readme), e.Key) {
			t.Errorf("key %q is seeded but missing from the README seed table", e.Key)
		}
	}
}
