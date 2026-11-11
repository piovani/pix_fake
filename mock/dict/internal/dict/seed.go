package dict

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SeedResult lista as chaves criadas e as que já existiam numa execução de Seed.
type SeedResult struct {
	Created  []string
	Existing []string
}

// Seed registra vínculos prontos, com a chave já definida (inclusive EVP, que na
// API é gerada pelo DICT), para popular o banco com dados de teste.
//
// É idempotente: o RequestId de cada vínculo é derivado da chave, então rodar de
// novo não duplica nem altera nada, apenas reporta o que já existia. Todos os
// vínculos são validados antes de qualquer gravação.
func (s *Service) Seed(ctx context.Context, entries []Entry) (SeedResult, error) {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if seen[e.Key] {
			return SeedResult{}, fmt.Errorf("seed: duplicate key %q", e.Key)
		}
		seen[e.Key] = true
		if v := validateSeedEntry(e); len(v) > 0 {
			return SeedResult{}, fmt.Errorf("seed: entry %q is invalid: %s", e.Key, describe(v))
		}
	}

	now := s.now().UTC().Truncate(time.Millisecond)
	var res SeedResult
	for _, e := range entries {
		e.CreationDate, e.KeyOwnershipDate = now, now
		switch err := s.repo.Create(ctx, e, deterministicUUID("seed:"+e.Key)); {
		case err == nil:
			res.Created = append(res.Created, e.Key)
		case errors.Is(err, ErrKeyExists), errors.Is(err, ErrRequestIDUsed):
			res.Existing = append(res.Existing, e.Key)
		default:
			return res, fmt.Errorf("seed: create %q: %w", e.Key, err)
		}
	}
	return res, nil
}

// validateSeedEntry aplica as mesmas regras da criação, mas aceita chave EVP informada.
func validateSeedEntry(e Entry) violations {
	var v violations
	if !validKeyType(e.KeyType) {
		v.add("key_type", string(e.KeyType), "Unknown key type")
	} else {
		validateKey("key", e.KeyType, e.Key, &v)
		validateKeyOwner(e.KeyType, e.Key, e.Owner, &v)
	}
	validateAccount("account", e.Account, &v)
	validateOwner("owner", e.Owner, &v)
	if e.Account.Type == AccountSalary {
		v.add("account.accountType", string(e.Account.Type), "Entries cannot be created for accounts of type SLRY")
	}
	return v
}

func describe(v violations) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf("%s=%q: %s", x.Property, x.Value, x.Reason)
	}
	return strings.Join(parts, "; ")
}
