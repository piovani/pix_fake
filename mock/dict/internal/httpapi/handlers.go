package httpapi

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"pixfake/mock/dict/internal/dict"
)

var payerIDRe = regexp.MustCompile(`^([0-9]{11}|[A-Z0-9]{12}[0-9]{2})$`)

func (s *Server) newHeader(r *http.Request) header {
	return header{ResponseTime: isoTime(s.now()), CorrelationID: correlationID(r.Context())}
}

func (s *Server) createEntry(r *http.Request, caller string) (int, any, error) {
	var req createEntryRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	e, err := s.svc.CreateEntry(r.Context(), caller, dict.CreateEntryInput{
		Entry:     entryFromXML(req.Entry),
		Reason:    dict.Reason(req.Reason),
		RequestID: req.RequestID,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, createEntryResponse{header: s.newHeader(r), Entry: entryToXML(e)}, nil
}

func (s *Server) getEntry(r *http.Request, _ string) (int, any, error) {
	var v []dict.Violation
	participant := r.Header.Get("PI-RequestingParticipant")
	if !dict.ValidISPB(participant) {
		v = append(v, dict.Violation{Property: "PI-RequestingParticipant", Value: participant,
			Reason: "Header is required and must be an 8 character identifier"})
	}
	payer := r.Header.Get("PI-PayerId")
	if !payerIDRe.MatchString(payer) {
		v = append(v, dict.Violation{Property: "PI-PayerId", Value: payer,
			Reason: "Header is required and must be a CPF or CNPJ"})
	}
	if r.Header.Get("PI-EndToEndId") == "" {
		v = append(v, dict.Violation{Property: "PI-EndToEndId", Reason: "Header is required"})
	}
	includeStats := false
	if raw := r.URL.Query().Get("IncludeStatistics"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			v = append(v, dict.Violation{Property: "IncludeStatistics", Value: raw, Reason: "Value must be a boolean"})
		}
		includeStats = b
	}
	if len(v) > 0 {
		return 0, nil, &dict.Error{Kind: dict.KindBadRequest, Detail: "Request has invalid parameters", Violations: v}
	}

	e, err := s.svc.GetEntry(r.Context(), r.PathValue("Key"))
	if err != nil {
		return 0, nil, err
	}
	resp := getEntryResponse{header: s.newHeader(r), Entry: entryToXML(e)}
	if includeStats {
		resp.OwnerStatistics, resp.KeyStatistics = zeroStats(s.now())
	}
	return http.StatusOK, resp, nil
}

func (s *Server) updateEntry(r *http.Request, caller string) (int, any, error) {
	var req updateEntryRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	in := dict.UpdateEntryInput{Key: req.Key, Reason: dict.Reason(req.Reason)}
	if req.Account != nil {
		a := accountFromXML(*req.Account)
		in.Account = &a
	}
	if req.Owner != nil {
		o := ownerFromXML(*req.Owner)
		in.Owner = &o
	}
	e, err := s.svc.UpdateEntry(r.Context(), caller, r.PathValue("Key"), in)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, updateEntryResponse{header: s.newHeader(r), Entry: entryToXML(e)}, nil
}

func (s *Server) deleteEntry(r *http.Request, caller string) (int, any, error) {
	var req deleteEntryRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	key := r.PathValue("Key")
	err := s.svc.DeleteEntry(r.Context(), caller, key, dict.DeleteEntryInput{
		Key: req.Key, Participant: req.Participant, Reason: dict.Reason(req.Reason),
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, deleteEntryResponse{header: s.newHeader(r), Key: key}, nil
}

func (s *Server) checkKeys(r *http.Request, _ string) (int, any, error) {
	var req checkKeysRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	statuses, err := s.svc.CheckKeys(r.Context(), req.Keys.Key)
	if err != nil {
		return 0, nil, err
	}
	resp := checkKeysResponse{header: s.newHeader(r)}
	for _, st := range statuses {
		resp.Keys.Key = append(resp.Keys.Key, keyStatusXML{Key: st.Key, HasEntry: st.HasEntry})
	}
	return http.StatusOK, resp, nil
}

// --- XML <-> domain conversions ---

func accountFromXML(a accountXML) dict.Account {
	return dict.Account{
		Participant:   a.Participant,
		Branch:        a.Branch,
		AccountNumber: a.AccountNumber,
		Type:          dict.AccountType(a.AccountType),
		OpeningDate:   time.Time(a.OpeningDate),
	}
}

func ownerFromXML(o ownerXML) dict.Owner {
	return dict.Owner{Type: dict.PersonType(o.Type), TaxID: o.TaxIDNumber, Name: o.Name, TradeName: o.TradeName}
}

func entryFromXML(e entryXML) dict.Entry {
	return dict.Entry{
		Key:     e.Key,
		KeyType: dict.KeyType(e.KeyType),
		Account: accountFromXML(e.Account),
		Owner:   ownerFromXML(e.Owner),
	}
}

func entryToXML(e dict.Entry) entryXML {
	created, owned := isoTime(e.CreationDate), isoTime(e.KeyOwnershipDate)
	return entryXML{
		Key:     e.Key,
		KeyType: string(e.KeyType),
		Account: accountXML{
			Participant:   e.Account.Participant,
			Branch:        e.Account.Branch,
			AccountNumber: e.Account.AccountNumber,
			AccountType:   string(e.Account.Type),
			OpeningDate:   isoTime(e.Account.OpeningDate),
		},
		Owner: ownerXML{
			Type:        string(e.Owner.Type),
			TaxIDNumber: e.Owner.TaxID,
			Name:        e.Owner.Name,
			TradeName:   e.Owner.TradeName,
		},
		CreationDate:     &created,
		KeyOwnershipDate: &owned,
	}
}
