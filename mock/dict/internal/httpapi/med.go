package httpapi

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pixfake/mock/dict/internal/dict"
)

// --- query parameters ---

type query struct {
	r *http.Request
	v []dict.Violation
}

func (q *query) bad(name, value, reason string) {
	q.v = append(q.v, dict.Violation{Property: name, Value: value, Reason: reason})
}

func (q *query) str(name string) string { return q.r.URL.Query().Get(name) }

func (q *query) boolean(name string) *bool {
	raw := q.str(name)
	if raw == "" {
		return nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		q.bad(name, raw, "Value must be a boolean")
	}
	return &b
}

func (q *query) integer(name string) int {
	raw := q.str(name)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		q.bad(name, raw, "Value must be an integer")
	}
	return n
}

func (q *query) instant(name string) time.Time {
	raw := q.str(name)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		q.bad(name, raw, "Value must be a date-time")
	}
	return t
}

// list reads an array parameter, repeated (Status=OPEN&Status=CLOSED) or comma separated.
func (q *query) list(name string) []string {
	var out []string
	for _, raw := range q.r.URL.Query()[name] {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func (q *query) err() error {
	if len(q.v) == 0 {
		return nil
	}
	return &dict.Error{Kind: dict.KindBadRequest, Detail: "Request has invalid parameters", Violations: q.v}
}

// sameID checks that the id in the body is the one in the path.
func sameID(body, path string) error {
	if body != path {
		return &dict.Error{Kind: dict.KindBadRequest, Detail: "The id in the body does not match the id in the path"}
	}
	return nil
}

func at(t time.Time) *isoTime {
	v := isoTime(t)
	return &v
}

// --- infraction reports ---

func reportToXML(r dict.InfractionReport, details bool) infractionReportXML {
	out := infractionReportXML{
		TransactionID: r.TransactionID, Reason: string(r.Reason), SituationType: string(r.Situation), ID: r.ID,
		Status: string(r.Status), ReporterParticipant: r.Reporter, CounterpartyParticipant: r.Counterparty,
		FraudMarkerID: r.FraudMarkerID, AnalysisResult: string(r.AnalysisResult), InfractionAmount: dict.FormatAmount(r.Amount),
		CreationTime: at(r.Created), LastModified: at(r.Modified),
	}
	if details {
		out.ReportDetails, out.AnalysisDetails = r.ReportDetails, r.AnalysisDetails
	}
	if r.Contact != nil {
		out.ContactInformation = &contactXML{Email: r.Contact.Email, Phone: r.Contact.Phone}
	}
	return out
}

func contactFromXML(c *contactXML) *dict.Contact {
	if c == nil {
		return nil
	}
	return &dict.Contact{Email: c.Email, Phone: c.Phone}
}

func (s *Server) reportResponse(r *http.Request, root string, status int, rep dict.InfractionReport, err error) (int, any, error) {
	if err != nil {
		return 0, nil, err
	}
	return status, infractionReportResponse{XMLName: xml.Name{Local: root}, header: s.newHeader(r), InfractionReport: reportToXML(rep, true)}, nil
}

func (s *Server) createInfractionReport(r *http.Request, caller string) (int, any, error) {
	var req createInfractionReportRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	in := req.InfractionReport
	rep, err := s.svc.CreateInfractionReport(r.Context(), caller, dict.CreateReportInput{
		Participant: req.Participant, TransactionID: in.TransactionID, Reason: dict.InfractionReason(in.Reason),
		Situation: dict.SituationType(in.SituationType), ReportDetails: in.ReportDetails, Contact: contactFromXML(in.ContactInformation),
	})
	return s.reportResponse(r, "CreateInfractionReportResponse", http.StatusCreated, rep, err)
}

func (s *Server) getInfractionReport(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	rep, err := s.svc.GetInfractionReport(r.Context(), caller, r.PathValue("InfractionReportId"))
	return s.reportResponse(r, "GetInfractionReportResponse", http.StatusOK, rep, err)
}

func (s *Server) updateInfractionReport(r *http.Request, caller string) (int, any, error) {
	var req updateInfractionReportRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("InfractionReportId")
	if err := sameID(req.InfractionReportID, id); err != nil {
		return 0, nil, err
	}
	rep, err := s.svc.UpdateInfractionReport(r.Context(), caller, id, dict.UpdateReportInput{
		Participant: req.Participant, Situation: dict.SituationType(req.SituationType), ReportDetails: req.ReportDetails,
		Contact: contactFromXML(req.ContactInformation),
	})
	return s.reportResponse(r, "UpdateInfractionReportResponse", http.StatusOK, rep, err)
}

func (s *Server) acknowledgeInfractionReport(r *http.Request, caller string) (int, any, error) {
	var req reportActionRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("InfractionReportId")
	if err := sameID(req.InfractionReportID, id); err != nil {
		return 0, nil, err
	}
	rep, err := s.svc.AcknowledgeInfractionReport(r.Context(), caller, req.Participant, id)
	return s.reportResponse(r, "AcknowledgeInfractionReportResponse", http.StatusOK, rep, err)
}

func (s *Server) cancelInfractionReport(r *http.Request, caller string) (int, any, error) {
	var req reportActionRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("InfractionReportId")
	if err := sameID(req.InfractionReportID, id); err != nil {
		return 0, nil, err
	}
	rep, err := s.svc.CancelInfractionReport(r.Context(), caller, req.Participant, id)
	return s.reportResponse(r, "CancelInfractionReportResponse", http.StatusOK, rep, err)
}

func (s *Server) closeInfractionReport(r *http.Request, caller string) (int, any, error) {
	var req closeInfractionReportRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("InfractionReportId")
	if err := sameID(req.InfractionReportID, id); err != nil {
		return 0, nil, err
	}
	rep, err := s.svc.CloseInfractionReport(r.Context(), caller, id, dict.CloseReportInput{
		Participant: req.Participant, Result: dict.AnalysisResult(req.AnalysisResult), FraudType: dict.FraudType(req.FraudType),
		AnalysisDetails: strings.TrimSpace(req.AnalysisDetails),
	})
	return s.reportResponse(r, "CloseInfractionReportResponse", http.StatusOK, rep, err)
}

func (s *Server) listInfractionReports(r *http.Request, caller string) (int, any, error) {
	q := &query{r: r}
	in := dict.ListReportsInput{
		Participant: q.str("Participant"), IsReporter: q.boolean("IsReporter"), IsCounterparty: q.boolean("IsCounterparty"),
		ModifiedAfter: q.instant("ModifiedAfter"), ModifiedBefore: q.instant("ModifiedBefore"), Limit: q.integer("Limit"),
	}
	for _, st := range q.list("Status") {
		in.Status = append(in.Status, dict.ReportStatus(st))
	}
	details := q.boolean("IncludeDetails")
	q.boolean("IncludeIndirectParticipants") // no indirect participants in the mock
	if err := q.err(); err != nil {
		return 0, nil, err
	}
	reports, more, err := s.svc.ListInfractionReports(r.Context(), caller, in)
	if err != nil {
		return 0, nil, err
	}
	resp := listInfractionReportsResponse{header: s.newHeader(r), HasMoreElements: more}
	for _, rep := range reports {
		resp.InfractionReports = append(resp.InfractionReports, reportToXML(rep, details != nil && *details))
	}
	return http.StatusOK, resp, nil
}

// --- fraud markers ---

func markerToXML(m dict.FraudMarker, withReporter bool) fraudMarkerXML {
	out := fraudMarkerXML{
		ID: m.ID, Status: string(m.Status), TaxIDNumber: m.TaxID, FraudType: string(m.FraudType), Key: m.Key,
		CreationTime: at(m.Created), LastModified: at(m.Modified),
	}
	if m.ReportID != "" {
		out.InfractionReport = &fraudMarkerReportXML{ID: m.ReportID}
		if withReporter {
			out.InfractionReport.ReporterParticipant = m.Reporter
		}
	}
	return out
}

func (s *Server) markerResponse(r *http.Request, root string, status int, m dict.FraudMarker, err error) (int, any, error) {
	if err != nil {
		return 0, nil, err
	}
	return status, fraudMarkerResponse{XMLName: xml.Name{Local: root}, header: s.newHeader(r), FraudMarker: markerToXML(m, true)}, nil
}

func (s *Server) createFraudMarker(r *http.Request, caller string) (int, any, error) {
	var req createFraudMarkerRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	m, err := s.svc.CreateFraudMarker(r.Context(), caller, dict.CreateMarkerInput{
		Participant: req.Participant, TaxID: req.FraudMarker.TaxIDNumber, Key: req.FraudMarker.Key,
		FraudType: dict.FraudType(req.FraudMarker.FraudType), RequestID: req.RequestID,
	})
	return s.markerResponse(r, "CreateFraudMarkerResponse", http.StatusCreated, m, err)
}

func (s *Server) getFraudMarker(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	m, err := s.svc.GetFraudMarker(r.Context(), caller, r.PathValue("FraudMarkerId"))
	return s.markerResponse(r, "GetFraudMarkerResponse", http.StatusOK, m, err)
}

func (s *Server) cancelFraudMarker(r *http.Request, caller string) (int, any, error) {
	var req cancelFraudMarkerRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("FraudMarkerId")
	if err := sameID(req.FraudMarkerID, id); err != nil {
		return 0, nil, err
	}
	m, err := s.svc.CancelFraudMarker(r.Context(), caller, req.Participant, id)
	return s.markerResponse(r, "CancelFraudMarkerResponse", http.StatusOK, m, err)
}

func (s *Server) listFraudMarkers(r *http.Request, caller string) (int, any, error) {
	q := &query{r: r}
	in := dict.ListMarkersInput{
		Participant: q.str("Participant"), TaxID: q.str("TaxIdNumber"), Key: q.str("Key"), Status: dict.MarkerStatus(q.str("Status")),
		ModifiedAfter: q.instant("ModifiedAfter"), ModifiedBefore: q.instant("ModifiedBefore"), Limit: q.integer("Limit"),
		Page: q.integer("page.pageNumber"), PerPage: q.integer("page.itemsPerPage"),
	}
	if err := q.err(); err != nil {
		return 0, nil, err
	}
	page, err := s.svc.ListFraudMarkers(r.Context(), caller, in)
	if err != nil {
		return 0, nil, err
	}
	resp := listFraudMarkersResponse{header: s.newHeader(r), HasMoreElements: page.HasMore, Total: page.Total}
	for _, m := range page.Markers {
		resp.FraudMarkers = append(resp.FraudMarkers, markerToXML(m, false))
	}
	return http.StatusOK, resp, nil
}

// --- refunds ---

func refundToXML(f dict.Refund, details bool) refundXML {
	out := refundXML{
		TransactionID: f.TransactionID, RefundReason: string(f.Reason), RefundAmount: dict.FormatAmount(f.Amount), ID: f.ID,
		Status: string(f.Status), ContestedParticipant: f.Contested, RequestingParticipant: f.Requesting,
		InfractionReportID: f.InfractionReportID, AnalysisResult: string(f.AnalysisResult),
		RefundRejectionReason: string(f.RejectionReason), RefundTransactionID: f.RefundTransactionID,
		CreationTime: at(f.Created), LastModified: at(f.Modified),
	}
	if details {
		out.RefundDetails, out.AnalysisDetails = f.Details, f.AnalysisDetails
	}
	return out
}

func (s *Server) refundResponse(r *http.Request, root string, status int, f dict.Refund, err error) (int, any, error) {
	if err != nil {
		return 0, nil, err
	}
	return status, refundResponse{XMLName: xml.Name{Local: root}, header: s.newHeader(r), Refund: refundToXML(f, true)}, nil
}

func (s *Server) createRefund(r *http.Request, caller string) (int, any, error) {
	var req createRefundRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	amount, err := dict.ParseAmount(req.Refund.RefundAmount)
	if err != nil {
		return 0, nil, &dict.Error{Kind: dict.KindBadRequest, Detail: "Refund has invalid fields", Violations: []dict.Violation{{
			Property: "refund.refundAmount", Value: req.Refund.RefundAmount, Reason: "Value must be an amount with at most two decimals",
		}}}
	}
	f, err := s.svc.CreateRefund(r.Context(), caller, dict.CreateRefundInput{
		Participant: req.Participant, TransactionID: req.Refund.TransactionID, Reason: dict.RefundReason(req.Refund.RefundReason),
		Amount: amount, Details: strings.TrimSpace(req.Refund.RefundDetails),
	})
	return s.refundResponse(r, "CreateRefundResponse", http.StatusCreated, f, err)
}

func (s *Server) getRefund(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.GetRefund(r.Context(), caller, r.PathValue("RefundId"))
	return s.refundResponse(r, "GetRefundResponse", http.StatusOK, f, err)
}

func (s *Server) cancelRefund(r *http.Request, caller string) (int, any, error) {
	var req cancelRefundRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("RefundId")
	if err := sameID(req.RefundID, id); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.CancelRefund(r.Context(), caller, req.Participant, id)
	return s.refundResponse(r, "CancelRefundResponse", http.StatusOK, f, err)
}

func (s *Server) closeRefund(r *http.Request, caller string) (int, any, error) {
	var req closeRefundRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("RefundId")
	if err := sameID(req.RefundID, id); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.CloseRefund(r.Context(), caller, id, dict.CloseRefundInput{
		Participant: req.Participant, Result: dict.RefundResult(req.RefundAnalysisResult),
		AnalysisDetails: strings.TrimSpace(req.RefundAnalysisDetails), RejectionReason: dict.RejectionReason(req.RefundRejectionReason),
		RefundTransactionID: req.RefundTransactionID,
	})
	return s.refundResponse(r, "CloseRefundResponse", http.StatusOK, f, err)
}

func (s *Server) listRefunds(r *http.Request, caller string) (int, any, error) {
	q := &query{r: r}
	in := dict.ListRefundsInput{
		Participant: q.str("Participant"), Role: dict.RefundRole(q.str("ParticipantRole")),
		ModifiedAfter: q.instant("ModifiedAfter"), ModifiedBefore: q.instant("ModifiedBefore"), Limit: q.integer("Limit"),
		Cursor: q.str("Cursor"),
	}
	for _, st := range q.list("Status") {
		in.Status = append(in.Status, dict.RefundStatus(st))
	}
	details := q.boolean("IncludeDetails")
	q.boolean("IncludeIndirectParticipants")
	if err := q.err(); err != nil {
		return 0, nil, err
	}
	page, err := s.svc.ListRefunds(r.Context(), caller, in)
	if err != nil {
		return 0, nil, err
	}
	resp := listRefundsResponse{header: s.newHeader(r), HasMoreElements: page.HasMore, NextCursor: page.NextCursor}
	for _, f := range page.Refunds {
		resp.Refunds = append(resp.Refunds, refundToXML(f, details != nil && *details))
	}
	return http.StatusOK, resp, nil
}

// --- statistics ---

func w(c dict.Windowed) windowed { return windowed{D90: int(c.D90), M12: int(c.M12), M60: int(c.M60)} }

func fillStats(c *commonStats, st dict.Statistics) {
	wm := isoTime(st.Watermark)
	c.Spi.Watermark, c.Spi.Settlements = wm, w(st.Settlements)
	f := &c.FraudMarkers
	f.Watermark, f.ApplicationFrauds, f.MuleAccounts, f.ScammerAccounts = wm, w(st.ApplicationFrauds), w(st.MuleAccounts), w(st.ScammerAccounts)
	f.OtherFrauds, f.UnknownFrauds = w(st.OtherFrauds), w(st.UnknownFrauds)
	f.TotalFraudTransactionAmount, f.DistinctFraudReporters = w(st.TotalFraudTransactionAmount), w(st.DistinctFraudReporters)
	i := &c.InfractionReports
	i.Watermark, i.OpenReports, i.OpenReportsDistinctReporters = wm, int(st.OpenReports), int(st.OpenReportsDistinctReporters)
	i.RejectedReports = w(st.RejectedReports)
}

func ownerStats(st dict.Statistics) *ownerStatsXML {
	var o ownerStatsXML
	fillStats(&o.commonStats, st)
	o.Entries.Watermark, o.Entries.RegisteredAccounts = isoTime(st.Watermark), int(st.RegisteredAccounts)
	return &o
}

func keyStats(st dict.Statistics) *keyStatsXML {
	var k keyStatsXML
	fillStats(&k.commonStats, st)
	k.Entries.Watermark, k.Entries.DistinctAccounts = isoTime(st.Watermark), w(st.DistinctAccounts)
	return &k
}

func (s *Server) getPersonStatistics(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	taxID := r.PathValue("TaxIdNumber")
	st, err := s.svc.PersonStatistics(r.Context(), taxID)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, getPersonStatisticsResponse{header: s.newHeader(r), TaxIDNumber: taxID, PersonStatistics: ownerStats(st)}, nil
}

func (s *Server) getEntryStatistics(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	key := r.PathValue("Key")
	owner, ks, err := s.svc.EntryStatistics(r.Context(), key)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, getEntryStatisticsResponse{header: s.newHeader(r), Key: key, OwnerStatistics: ownerStats(owner), KeyStatistics: keyStats(ks)}, nil
}
