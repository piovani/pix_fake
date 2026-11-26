package dict

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	defaultMarkersPerPage = 10
	maxMarkerPage         = 200
)

func markerNotFound() error { return newError(KindNotFound, "Fraud marker not found") }

// visibleMarker returns the marker, or NotFound when the caller is not its registrant nor the reporter of the
// infraction report it came from.
func visibleMarker(m FraudMarker, err error, caller string) (FraudMarker, error) {
	if errors.Is(err, ErrNotFound) || err == nil && caller != m.Registrant && (m.Reporter == "" || caller != m.Reporter) {
		return FraudMarker{}, markerNotFound()
	}
	return m, err
}

func validFraudType(t FraudType) bool {
	return slices.Contains([]FraudType{FraudApplication, FraudMule, FraudScammer, FraudOther}, t)
}

// checkKeyOwner refuses a key that is registered to someone else than the user: a marker on a key is a marker on its
// owner. A key that is not registered (any more) is accepted, since the fraud may be older than its deletion.
func (s *Service) checkKeyOwner(ctx context.Context, property, key, taxID string, v *violations) error {
	if key == "" {
		return nil
	}
	if _, ok := DetectKeyType(key); !ok {
		v.add(property, key, "Value is not a valid CPF, CNPJ, PHONE, EMAIL or EVP key")
		return nil
	}
	e, err := s.repo.Get(ctx, key)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	case e.Owner.TaxID != taxID:
		v.add(property, key, "Key does not belong to the user of TaxIdNumber")
	}
	return nil
}

type CreateMarkerInput struct {
	Participant string
	TaxID       string
	Key         string
	FraudType   FraudType
	RequestID   string
}

// CreateFraudMarker marks one of the caller's users as involved in a fraud. It is idempotent per (caller, RequestId).
func (s *Service) CreateFraudMarker(ctx context.Context, caller string, in CreateMarkerInput) (FraudMarker, error) {
	var v violations
	validateRegexp("participant", in.Participant, ispbRe, &v)
	validateRegexp("fraudMarker.taxIdNumber", in.TaxID, taxIDRe, &v)
	if !validFraudType(in.FraudType) {
		v.add("fraudMarker.fraudType", string(in.FraudType), "Invalid fraud type, valid values: APPLICATION_FRAUD, MULE_ACCOUNT, SCAMMER_ACCOUNT, OTHER")
	}
	validateRegexp("requestId", in.RequestID, requestIDRe, &v)
	if err := s.checkKeyOwner(ctx, "fraudMarker.key", in.Key, in.TaxID, &v); err != nil {
		return FraudMarker{}, err
	}
	if len(v) > 0 {
		return FraudMarker{}, invalid(KindBadRequest, "FraudMarker has invalid fields", v)
	}
	if in.Participant != caller {
		return FraudMarker{}, newError(KindForbidden, "Participant is not allowed to create fraud markers for another participant")
	}
	var out FraudMarker
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		prev, err := tx.MarkerByRequestID(ctx, caller, in.RequestID)
		if err == nil {
			if prev.TaxID != in.TaxID || prev.Key != in.Key || prev.FraudType != in.FraudType {
				return newError(KindRequestIDAlreadyUsed, "RequestId was already used with different parameters")
			}
			out = prev
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		now := s.stamp()
		out = FraudMarker{
			ID: s.newID(), Registrant: caller, TaxID: in.TaxID, Key: in.Key, FraudType: in.FraudType, Status: MarkerRegistered,
			RequestID: in.RequestID, Created: now, Modified: now,
		}
		return tx.SaveMarker(ctx, out)
	})
	return out, err
}

// GetFraudMarker returns a marker the caller registered, or that came from one of its infraction reports.
func (s *Service) GetFraudMarker(ctx context.Context, caller, id string) (FraudMarker, error) {
	var out FraudMarker
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		m, err := tx.Marker(ctx, id)
		out, err = visibleMarker(m, err, caller)
		return err
	})
	return out, err
}

// CancelFraudMarker is the registrant withdrawing its marker. Repeating it is harmless.
func (s *Service) CancelFraudMarker(ctx context.Context, caller, participant, id string) (FraudMarker, error) {
	if participant != caller {
		return FraudMarker{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	var out FraudMarker
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		m, err := tx.Marker(ctx, id)
		if m, err = visibleMarker(m, err, caller); err != nil {
			return err
		}
		if caller != m.Registrant {
			return newError(KindForbidden, "Only the participant that registered the fraud marker can cancel it")
		}
		if m.Status != MarkerCancelled {
			m.Status, m.Modified = MarkerCancelled, s.stamp()
			if err := tx.SaveMarker(ctx, m); err != nil {
				return err
			}
		}
		out = m
		return nil
	})
	return out, err
}

type ListMarkersInput struct {
	Participant    string // optional; when given, the caller
	TaxID          string
	Key            string
	Status         MarkerStatus
	ModifiedAfter  time.Time
	ModifiedBefore time.Time
	Limit          int // the size of the set the pages are cut from, 20 by default
	Page           int
	PerPage        int // 10 by default
}

type MarkersPage struct {
	Markers []FraudMarker
	HasMore bool
	Total   int // of the set, at most Limit
}

// ListFraudMarkers lists the markers the caller registered on a user, or on one of its keys, oldest change first.
func (s *Service) ListFraudMarkers(ctx context.Context, caller string, in ListMarkersInput) (MarkersPage, error) {
	var v violations
	if in.Participant != "" {
		validateRegexp("Participant", in.Participant, ispbRe, &v)
	}
	validateRegexp("TaxIdNumber", in.TaxID, taxIDRe, &v)
	if in.Status != "" && in.Status != MarkerRegistered && in.Status != MarkerCancelled {
		v.add("Status", string(in.Status), "Invalid status, valid values: REGISTERED, CANCELLED")
	}
	limit := listLimit(in.Limit, &v)
	if in.Page < 0 || in.Page > maxMarkerPage {
		v.add("page.pageNumber", fmt.Sprint(in.Page), "Value must be between 0 and 200")
	}
	switch {
	case in.PerPage == 0:
		in.PerPage = defaultMarkersPerPage
	case in.PerPage < 1 || in.PerPage > maxMarkerPage:
		v.add("page.itemsPerPage", fmt.Sprint(in.PerPage), "Value must be between 1 and 200")
	}
	if err := s.checkKeyOwner(ctx, "Key", in.Key, in.TaxID, &v); err != nil {
		return MarkersPage{}, err
	}
	if len(v) > 0 {
		return MarkersPage{}, invalid(KindBadRequest, "Request has invalid parameters", v)
	}
	if in.Participant != "" && in.Participant != caller {
		return MarkersPage{}, newError(KindForbidden, "Participant is not allowed to list the fraud markers of another participant")
	}
	all, err := s.repo.ListMarkers(ctx, MarkerQuery{TaxID: in.TaxID, Key: in.Key})
	if err != nil {
		return MarkersPage{}, err
	}
	var set []FraudMarker
	for _, m := range all {
		if m.Registrant != caller || in.Status != "" && m.Status != in.Status || !within(m.Modified, in.ModifiedAfter, in.ModifiedBefore) {
			continue
		}
		if len(set) == limit {
			break
		}
		set = append(set, m)
	}
	page := MarkersPage{Total: len(set)}
	start := min(in.Page*in.PerPage, len(set))
	end := min(start+in.PerPage, len(set))
	page.Markers, page.HasMore = set[start:end], end < len(set)
	return page, nil
}
