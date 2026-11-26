package pixkey

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

// Divergence is how the wallet's copy of a key disagrees with the DICT.
type Divergence string

const (
	// MissingInDict: the wallet has the key and the DICT does not, such as a key removed from the DICT behind the wallet's
	// back. The wallet pays it as a book transfer while other banks are told it does not exist.
	MissingInDict Divergence = "MISSING_IN_DICT"
	// DataDiffers: both have the key but not with the same data (account, holder, or the RequestId that created it).
	DataDiffers Divergence = "DATA_DIFFERS"
	// OnlyInDict: the DICT has a key under the wallet that the wallet does not know, such as a registration that reached
	// the DICT but not the wallet's records. Payments to it arrive and the wallet cannot tell whose it is.
	OnlyInDict Divergence = "ONLY_IN_DICT"
)

var divergences = []Divergence{MissingInDict, DataDiffers, OnlyInDict}

type Finding struct {
	Kind      Divergence
	Key       string
	Type      pix.KeyType
	AccountID string // empty for OnlyInDict
}

const (
	checkBatch   = 200 // the most keys checkKeys takes
	filePoll     = time.Second
	fileDeadline = 2 * time.Minute
)

var (
	keyTypes = []pix.KeyType{pix.KeyCPF, pix.KeyCNPJ, pix.KeyPhone, pix.KeyEmail, pix.KeyEVP}

	outOfSync, _ = otel.Meter("pixfake/wallet/pixkey").Int64Gauge("wallet.keys.out_of_sync",
		metric.WithDescription("keys of the wallet that disagree with the DICT at the last reconciliation, by kind"))
)

type local struct {
	key Key
	cid string
}

// Check reconciles the wallet's copy of its keys with the DICT, the way the DICT API's Reconciliation describes: for each
// key type it compares the VSync (the XOR of the CIDs) of both sides, and only when they differ it fetches the DICT's file
// of CIDs to find which keys disagree. A CID covers the key, its account, its holder and the RequestId that created it, so
// any difference in them shows up.
func (s *Service) Check(ctx context.Context) ([]Finding, error) {
	byType, err := s.locals(ctx)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, kt := range keyTypes {
		f, err := s.checkType(ctx, kt, byType[kt])
		if err != nil {
			return nil, fmt.Errorf("pixkey: reconcile %s keys: %w", kt, err)
		}
		findings = append(findings, f...)
	}
	for _, d := range divergences {
		n := 0
		for _, f := range findings {
			if f.Kind == d {
				n++
			}
		}
		outOfSync.Record(ctx, int64(n), metric.WithAttributes(attribute.String("kind", string(d))))
	}
	return findings, nil
}

// locals computes the CID of every key of the wallet, from its own records.
func (s *Service) locals(ctx context.Context) (map[pix.KeyType][]local, error) {
	accounts := map[string]ledger.Account{}
	out := map[pix.KeyType][]local{}
	for after := ""; ; {
		page, err := s.store.Page(ctx, after, checkBatch)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, k := range page {
			acct, ok := accounts[k.AccountID]
			if !ok {
				if acct, err = s.accounts.Account(ctx, k.AccountID); err != nil {
					return nil, err
				}
				accounts[k.AccountID] = acct
			}
			cid, err := pix.CID(s.entry(RegisterInput{Type: k.Type, Key: k.Key}, acct), k.RequestID)
			if err != nil {
				return nil, err
			}
			out[k.Type] = append(out[k.Type], local{key: k, cid: cid})
		}
		after = page[len(page)-1].Key
	}
}

func (s *Service) checkType(ctx context.Context, kt pix.KeyType, mine []local) ([]Finding, error) {
	var v pix.VSync
	for _, l := range mine {
		if err := v.Add(l.cid); err != nil {
			return nil, err
		}
	}
	result, err := s.dict.CreateSyncVerification(ctx, kt, v.String())
	if err != nil || result == pix.SyncOK {
		return nil, err
	}

	theirs, err := s.dictCIDs(ctx, kt)
	if err != nil {
		return nil, err
	}
	inDict := make(map[string]bool, len(theirs))
	for _, c := range theirs {
		inDict[c] = true
	}
	known := make(map[string]bool, len(mine))
	byKey := make(map[string]Key, len(mine))
	var differ []Key // in the wallet with a CID the DICT does not have
	for _, l := range mine {
		known[l.cid], byKey[l.key.Key] = true, l.key
		if !inDict[l.cid] {
			differ = append(differ, l.key)
		}
	}

	var findings []Finding
	// checkKeys tells a key the DICT lacks from one it has with other data
	for batch := range slices.Chunk(differ, checkBatch) {
		keys := make([]string, len(batch))
		for i, k := range batch {
			keys[i] = k.Key
		}
		statuses, err := s.dict.CheckKeys(ctx, keys)
		if err != nil {
			return nil, err
		}
		has := make(map[string]bool, len(statuses))
		for _, st := range statuses {
			has[st.Key] = st.HasEntry
		}
		for _, k := range batch {
			kind := MissingInDict
			if has[k.Key] {
				kind = DataDiffers
			}
			findings = append(findings, Finding{Kind: kind, Key: k.Key, Type: k.Type, AccountID: k.AccountID})
		}
	}
	// a CID only the DICT has is either the other side of a DataDiffers, or a key the wallet does not know
	for _, c := range theirs {
		if known[c] {
			continue
		}
		e, _, err := s.dict.GetEntryByCID(ctx, c)
		if err != nil {
			return nil, err
		}
		if _, ours := byKey[e.Key]; !ours {
			findings = append(findings, Finding{Kind: OnlyInDict, Key: e.Key, Type: kt})
		}
	}
	return findings, nil
}

// dictCIDs asks the DICT for its file of the wallet's CIDs of one key type and reads it.
func (s *Service) dictCIDs(ctx context.Context, kt pix.KeyType) ([]string, error) {
	f, err := s.dict.CreateCIDFile(ctx, kt)
	if err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, fileDeadline)
	defer cancel()
	if f.Status != pix.FileAvailable {
		if f, err = s.dict.WaitCIDFile(wait, f.ID, filePoll); err != nil {
			return nil, err
		}
	}
	return s.dict.DownloadCIDFile(ctx, f)
}
