package httpapi

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pixfake/mock/dict/internal/dict"
)

// --- funds recoveries ---

func recoveryToXML(f dict.FundsRecovery) fundsRecoveryXML {
	out := fundsRecoveryXML{
		RootTransactionID: f.RootTransactionID, SituationType: string(f.Situation), ReportDetails: f.ReportDetails, ID: f.ID,
		Status: string(f.Status), ReporterParticipant: f.Reporter, CreationTime: at(f.Created), LastModified: at(f.Modified),
	}
	if f.Contact != nil {
		out.ContactInformation = &contactXML{Email: f.Contact.Email, Phone: f.Contact.Phone}
	}
	return out
}

func parametersToXML(p dict.GraphParameters) graphParametersXML {
	return graphParametersXML{
		MinTransactionAmount: dict.FormatAmount(p.MinAmount), MaxTransactions: strconv.Itoa(p.MaxTransactions),
		HopWindow: dict.FormatDuration(p.HopWindow), MaxHops: strconv.Itoa(p.MaxHops),
	}
}

func graphToXML(f dict.FundsRecovery) trackingGraphXML {
	g := f.Graph
	out := trackingGraphXML{
		FundsRecoveryID: f.ID, RootTransactionID: f.RootTransactionID, CreationTime: isoTime(g.Created), Parameters: parametersToXML(g.Parameters),
	}
	for _, p := range g.Persons {
		kind := "NATURAL_PERSON"
		if p.Legal {
			kind = "LEGAL_PERSON"
		}
		out.Persons = append(out.Persons, graphPersonXML{ID: p.ID, Type: kind})
	}
	for _, a := range g.Accounts {
		out.Accounts = append(out.Accounts, graphAccountXML{ID: a.ID, OwnerID: a.OwnerID, Participant: a.Participant})
	}
	for _, t := range g.Transactions {
		out.Transactions = append(out.Transactions, graphTransactionXML{
			ID: t.ID, DebtorAccountID: t.DebtorAccountID, CreditorAccountID: t.CreditorAccountID, Amount: dict.FormatAmount(t.Amount),
			RefundableAmount: dict.FormatAmount(t.Refundable), SettlementTime: isoTime(t.SettlementTime), TransactionDepth: t.Depth,
		})
	}
	return out
}

// graphInput reads the graph parameters of a request; a value that does not parse is a violation of its own kind.
func graphInput(x *graphParametersXML) (dict.GraphInput, error) {
	var in dict.GraphInput
	if x == nil {
		return in, nil
	}
	var v []dict.Violation
	bad := func(property, value, reason string) {
		v = append(v, dict.Violation{Property: property, Value: value, Reason: reason})
	}
	integer := func(property, raw string) *int {
		if raw = strings.TrimSpace(raw); raw == "" {
			return nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			bad(property, raw, "Value must be an integer")
			return nil
		}
		return &n
	}
	if raw := strings.TrimSpace(x.MinTransactionAmount); raw != "" {
		if c, err := dict.ParseAmount(raw); err != nil {
			bad("MinTransactionAmount", raw, "Value must be an amount with at most two decimals")
		} else {
			in.MinAmount = &c
		}
	}
	in.MaxTransactions = integer("MaxTransactions", x.MaxTransactions)
	if raw := strings.TrimSpace(x.HopWindow); raw != "" {
		if d, err := dict.ParseDuration(raw); err != nil {
			bad("HopWindow", raw, "Value must be an ISO 8601 duration, such as PT24H")
		} else {
			in.HopWindow = &d
		}
	}
	in.MaxHops = integer("MaxHops", x.MaxHops)
	if len(v) > 0 {
		return in, &dict.Error{Kind: dict.KindRecoveryGraphInvalid, Detail: "TrackingGraphParameters has invalid values", Violations: v}
	}
	return in, nil
}

func (s *Server) recoveryResponse(r *http.Request, root string, f dict.FundsRecovery, err error) (int, any, error) {
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, fundsRecoveryResponse{XMLName: xml.Name{Local: root}, header: s.newHeader(r), FundsRecovery: recoveryToXML(f)}, nil
}

func (s *Server) createFundsRecovery(r *http.Request, caller string) (int, any, error) {
	var req createFundsRecoveryRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	graph, err := graphInput(req.TrackingGraphParameters)
	if err != nil {
		return 0, nil, err
	}
	in := req.FundsRecovery
	f, err := s.svc.CreateFundsRecovery(r.Context(), caller, dict.CreateRecoveryInput{
		Participant: req.Participant, FlowType: in.FlowType, RootTransactionID: in.RootTransactionID,
		Situation: dict.SituationType(in.SituationType), ReportDetails: in.ReportDetails, Contact: contactFromXML(in.ContactInformation),
		Graph: graph,
	})
	if err != nil {
		return 0, nil, err
	}
	g := graphToXML(f)
	return http.StatusCreated, fundsRecoveryResponse{
		XMLName: xml.Name{Local: "CreateFundsRecoveryResponse"}, header: s.newHeader(r), FundsRecovery: recoveryToXML(f), TrackingGraph: &g,
	}, nil
}

func (s *Server) getFundsRecovery(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.GetFundsRecovery(r.Context(), caller, r.PathValue("FundsRecoveryId"))
	return s.recoveryResponse(r, "GetFundsRecoveryResponse", f, err)
}

func (s *Server) updateFundsRecovery(r *http.Request, caller string) (int, any, error) {
	var req updateFundsRecoveryRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("FundsRecoveryId")
	if err := sameID(req.FundsRecoveryID, id); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.UpdateFundsRecovery(r.Context(), caller, id, dict.UpdateRecoveryInput{
		Participant: req.Participant, Situation: dict.SituationType(req.SituationType), ReportDetails: req.ReportDetails,
		Contact: contactFromXML(req.ContactInformation),
	})
	return s.recoveryResponse(r, "UpdateFundsRecoveryResponse", f, err)
}

// recoveryAction decodes the body of an action on a funds recovery and checks its id.
func recoveryAction(r *http.Request) (recoveryActionRequest, string, error) {
	var req recoveryActionRequest
	if err := decodeXML(r, &req); err != nil {
		return req, "", err
	}
	id := r.PathValue("FundsRecoveryId")
	return req, id, sameID(req.FundsRecoveryID, id)
}

func (s *Server) cancelFundsRecovery(r *http.Request, caller string) (int, any, error) {
	req, id, err := recoveryAction(r)
	if err != nil {
		return 0, nil, err
	}
	f, err := s.svc.CancelFundsRecovery(r.Context(), caller, req.Participant, id)
	return s.recoveryResponse(r, "CancelFundsRecoveryResponse", f, err)
}

func (s *Server) refundFundsRecovery(r *http.Request, caller string) (int, any, error) {
	req, id, err := recoveryAction(r)
	if err != nil {
		return 0, nil, err
	}
	f, err := s.svc.RefundFundsRecovery(r.Context(), caller, req.Participant, id)
	return s.recoveryResponse(r, "RefundFundsRecoveryResponse", f, err)
}

// deprecatedRecoveryOperation answers drawing the graph again and blocking, of the deprecated INTERACTIVE flow.
func (s *Server) deprecatedRecoveryOperation(r *http.Request, _ string) (int, any, error) {
	if _, _, err := recoveryAction(r); err != nil {
		return 0, nil, err
	}
	return 0, nil, s.svc.DeprecatedOperation()
}

func (s *Server) getTrackingGraph(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.GetTrackingGraph(r.Context(), caller, r.PathValue("FundsRecoveryId"))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, getTrackingGraphResponse{header: s.newHeader(r), TrackingGraph: graphToXML(f)}, nil
}

func (s *Server) listFundsRecoveryReports(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	reports, err := s.svc.FundsRecoveryReports(r.Context(), caller, r.PathValue("FundsRecoveryId"))
	if err != nil {
		return 0, nil, err
	}
	resp := listFundsRecoveryReportsResponse{header: s.newHeader(r)}
	for _, rep := range reports {
		resp.InfractionReports = append(resp.InfractionReports, reportToXML(rep, true))
	}
	return http.StatusOK, resp, nil
}

func (s *Server) listFundsRecoveryRefunds(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	refunds, err := s.svc.FundsRecoveryRefunds(r.Context(), caller, r.PathValue("FundsRecoveryId"))
	if err != nil {
		return 0, nil, err
	}
	resp := listFundsRecoveryRefundsResponse{header: s.newHeader(r)}
	for _, f := range refunds {
		resp.Refunds = append(resp.Refunds, refundToXML(f, true))
	}
	return http.StatusOK, resp, nil
}

// --- event notifications ---

func (s *Server) listEventNotifications(r *http.Request, caller string) (int, any, error) {
	q := &query{r: r}
	in := dict.ListEventsInput{Participant: q.str("Participant"), Cursor: q.str("Cursor")}
	q.boolean("IncludeIndirectParticipants") // no indirect participants in the mock
	if err := q.err(); err != nil {
		return 0, nil, err
	}
	page, err := s.svc.ListEventNotifications(r.Context(), caller, in)
	if err != nil {
		return 0, nil, err
	}
	resp := listEventNotificationsResponse{header: s.newHeader(r), HasMoreElements: page.HasMore, NextCursor: page.NextCursor}
	for _, e := range page.Events {
		resp.EventNotifications = append(resp.EventNotifications, eventNotificationXML{
			ID: e.ID, Event: string(e.Type), EntityType: dict.EntityFundsRecovery, EntityID: e.EntityID,
			Timestamp: isoTime(e.Time.Truncate(time.Millisecond)), Participant: e.Participant,
		})
	}
	return http.StatusOK, resp, nil
}
