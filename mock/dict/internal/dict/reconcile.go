package dict

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	defaultCIDEvents = 100
	maxCIDEvents     = 200
)

// vsyncOf is the DICT's VSync of the participant's entries of one key type.
func (s *Service) vsyncOf(ctx context.Context, participant string, kt KeyType) (string, []string, error) {
	cids, err := s.repo.CIDs(ctx, participant, kt)
	if err != nil {
		return "", nil, err
	}
	var v VSync
	for _, c := range cids {
		if err := v.Add(c); err != nil {
			return "", nil, err
		}
	}
	return v.String(), cids, nil
}

// checkScope validates the participant and key type of a reconciliation call: only the caller's own entries can be
// reconciled.
func checkScope(caller, participant string, kt KeyType, v *violations) error {
	if !ispbRe.MatchString(participant) {
		v.add("Participant", participant, "Participant must be an 8 character identifier")
	}
	if !validKeyType(kt) {
		v.add("KeyType", string(kt), "Unknown key type")
	}
	if len(*v) > 0 {
		return invalid(KindBadRequest, "Request has invalid fields", *v)
	}
	if participant != caller {
		return newError(KindForbidden, "Participant is not allowed to reconcile the entries of another participant")
	}
	return nil
}

// CreateSyncVerification compares the participant's VSync with the DICT's: OK when they are the same.
func (s *Service) CreateSyncVerification(ctx context.Context, caller string, in SyncVerification) (SyncVerification, error) {
	var v violations
	if !cidRe.MatchString(in.Verifier) {
		v.add("SyncVerification.ParticipantSyncVerifier", in.Verifier, "Sync verifier must be 64 hexadecimal characters")
	}
	if err := checkScope(caller, in.Participant, in.KeyType, &v); err != nil {
		return SyncVerification{}, err
	}
	vsync, _, err := s.vsyncOf(ctx, in.Participant, in.KeyType)
	if err != nil {
		return SyncVerification{}, err
	}
	in.DictVSync, in.Result = vsync, SyncNOK
	if strings.EqualFold(in.Verifier, vsync) {
		in.Result = SyncOK
	}
	return s.repo.CreateSyncVerification(ctx, in)
}

// CreateCIDFile writes the file of the participant's CIDs of one key type. The spec makes it asynchronous: the mock
// writes it at once but answers REQUESTED, as the DICT does, so the participant has to come back for it.
func (s *Service) CreateCIDFile(ctx context.Context, caller, participant string, kt KeyType) (CIDFile, error) {
	var v violations
	if err := checkScope(caller, participant, kt, &v); err != nil {
		return CIDFile{}, err
	}
	_, cids, err := s.vsyncOf(ctx, participant, kt)
	if err != nil {
		return CIDFile{}, err
	}
	var b strings.Builder
	for _, c := range cids {
		b.WriteString(c)
		b.WriteByte('\n')
	}
	content := []byte(b.String())
	sum := sha256.Sum256(content)
	now := s.now().UTC().Truncate(time.Millisecond)
	f, err := s.repo.CreateCIDFile(ctx, CIDFile{
		Status: FileAvailable, Participant: participant, KeyType: kt,
		RequestTime: now, CreationTime: now, Content: content, Sha256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return CIDFile{}, err
	}
	requested := f
	requested.Status, requested.CreationTime, requested.Content, requested.Sha256 = FileRequested, time.Time{}, nil, ""
	return requested, nil
}

// GetCIDFile returns one of the caller's CID files.
func (s *Service) GetCIDFile(ctx context.Context, caller string, id int64) (CIDFile, error) {
	f, err := s.repo.GetCIDFile(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return CIDFile{}, newError(KindNotFound, "CID file not found")
	}
	if err != nil {
		return CIDFile{}, err
	}
	if f.Participant != caller {
		return CIDFile{}, newError(KindForbidden, "Participant is not allowed to access this resource")
	}
	return f, nil
}

// CIDFileContent is what the download URL of a file serves. Like a signed storage URL, it needs no participant.
func (s *Service) CIDFileContent(ctx context.Context, id int64) ([]byte, error) {
	f, err := s.repo.GetCIDFile(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, newError(KindNotFound, "CID file not found")
	}
	return f.Content, err
}

// EntryByCID returns the entry of one of the caller's CIDs, with the RequestId that created it.
func (s *Service) EntryByCID(ctx context.Context, caller, cid string) (Entry, error) {
	if !cidRe.MatchString(cid) {
		return Entry{}, newError(KindNotFound, "Resource does not exist")
	}
	e, err := s.repo.GetByCID(ctx, strings.ToLower(cid))
	if err != nil {
		return Entry{}, mapNotFound(err)
	}
	if e.Account.Participant != caller {
		return Entry{}, newError(KindForbidden, "Participant is not allowed to access this resource")
	}
	return e, nil
}

type ListCIDEventsInput struct {
	Participant string
	KeyType     KeyType
	Start, End  time.Time // zero for no bound
	Limit       int       // 0 for the default
}

type CIDEventsPage struct {
	Events     []CIDEvent
	HasMore    bool
	Start, End time.Time
	// VSync after the first and after the last event of the page
	VerifierStart, VerifierEnd string
}

// ListCIDEvents lists the CID events of the caller's entries of one key type, oldest first, between Start and End.
func (s *Service) ListCIDEvents(ctx context.Context, caller string, in ListCIDEventsInput) (CIDEventsPage, error) {
	var v violations
	switch {
	case in.Limit == 0:
		in.Limit = defaultCIDEvents
	case in.Limit < 1 || in.Limit > maxCIDEvents:
		v.add("Limit", "", "Limit must be between 1 and 200")
	}
	if err := checkScope(caller, in.Participant, in.KeyType, &v); err != nil {
		return CIDEventsPage{}, err
	}
	events, err := s.repo.CIDEvents(ctx, in.Participant, in.KeyType)
	if err != nil {
		return CIDEventsPage{}, err
	}

	// The VSync at any moment is the XOR of every event until then: an addition and a removal are the same XOR.
	var running VSync
	page := CIDEventsPage{VerifierStart: ZeroVSync}
	for _, ev := range events {
		if !in.End.IsZero() && ev.Timestamp.After(in.End) {
			break
		}
		if err := running.Add(ev.CID); err != nil {
			return CIDEventsPage{}, err
		}
		if !in.Start.IsZero() && ev.Timestamp.Before(in.Start) {
			page.VerifierStart = running.String() // the VSync before the window, if it ends up empty
			continue
		}
		if len(page.Events) == in.Limit {
			page.HasMore = true
			break
		}
		page.Events = append(page.Events, ev)
		if len(page.Events) == 1 {
			page.VerifierStart = running.String()
		}
		page.VerifierEnd = running.String()
	}
	if len(page.Events) == 0 {
		page.VerifierEnd = page.VerifierStart
		page.Start, page.End = in.Start, in.Start
		if in.Start.IsZero() {
			now := s.now().UTC()
			page.Start, page.End = now, now
		}
		return page, nil
	}
	page.Start, page.End = page.Events[0].Timestamp, page.Events[len(page.Events)-1].Timestamp
	return page, nil
}
