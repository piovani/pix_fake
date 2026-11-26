package dict

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SeedResult lists the keys created and those that already existed in a Seed run.
type SeedResult struct {
	Created  []string
	Existing []string
}

// Seed registers ready-made entries, with the key already defined (including EVP, which
// the API generates on the DICT side), to populate the database with test data.
//
// It is idempotent: each entry's RequestId is derived from its key, so running it
// again neither duplicates nor changes anything, it only reports what already
// existed. All entries are validated before anything is written.
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
		e.RequestID = SeedRequestID(e.Key)
		cid, err := CID(e, e.RequestID)
		if err != nil {
			return res, err
		}
		e.CID = cid
		switch err := s.repo.Create(ctx, e, e.RequestID); {
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

// SeedRequestID is the RequestId a seed entry is created with. The wallet's own seed uses the same ones for its keys, so
// both sides compute the same CIDs.
func SeedRequestID(key string) string { return deterministicUUID("seed:" + key) }

// validateSeedEntry applies the same rules as creation, but accepts a supplied EVP key.
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
