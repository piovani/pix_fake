package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"pixfake/mock/dict/internal/dict"
)

// cidFilesPath serves the content of the CID files. It stands for the storage the DICT points the participant to: it is
// not part of the API and, like a signed URL, it asks for no participant.
const cidFilesPath = "/files/cids/"

func (s *Server) createSyncVerification(r *http.Request, caller string) (int, any, error) {
	var req createSyncVerificationRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	in := req.SyncVerification
	v, err := s.svc.CreateSyncVerification(r.Context(), caller, dict.SyncVerification{
		Participant: in.Participant, KeyType: dict.KeyType(in.KeyType), Verifier: in.ParticipantSyncVerifier,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, createSyncVerificationResponse{header: s.newHeader(r), SyncVerification: syncVerificationXML{
		Participant: v.Participant, KeyType: string(v.KeyType), ParticipantSyncVerifier: v.Verifier, ID: v.ID, Result: string(v.Result),
	}}, nil
}

func (s *Server) createCidSetFile(r *http.Request, caller string) (int, any, error) {
	var req createCidSetFileRequest
	if err := decodeXML(r, &req); err != nil {
		return 0, nil, err
	}
	f, err := s.svc.CreateCIDFile(r.Context(), caller, req.Participant, dict.KeyType(req.KeyType))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, createCidSetFileResponse{header: s.newHeader(r), CidSetFile: cidFileToXML(r, f)}, nil
}

func (s *Server) getCidSetFile(r *http.Request, caller string) (int, any, error) {
	if err := requestingParticipant(r, caller); err != nil {
		return 0, nil, err
	}
	id, err := strconv.ParseInt(r.PathValue("Id"), 10, 64)
	if err != nil {
		return 0, nil, &dict.Error{Kind: dict.KindNotFound, Detail: "CID file not found"}
	}
	f, err := s.svc.GetCIDFile(r.Context(), caller, id)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, getCidSetFileResponse{header: s.newHeader(r), CidSetFile: cidFileToXML(r, f)}, nil
}

// requestingParticipant checks PI-RequestingParticipant, which the spec asks for on getCidSetFile: it must be the caller.
func requestingParticipant(r *http.Request, caller string) error {
	if r.Header.Get("PI-RequestingParticipant") != caller {
		return &dict.Error{Kind: dict.KindForbidden, Detail: "PI-RequestingParticipant must be the participant making the request"}
	}
	return nil
}

func cidFileToXML(r *http.Request, f dict.CIDFile) cidSetFileXML {
	out := cidSetFileXML{
		ID: f.ID, Status: string(f.Status), Participant: f.Participant, KeyType: string(f.KeyType), RequestTime: isoTime(f.RequestTime),
	}
	if f.Status == dict.FileAvailable {
		created, size := isoTime(f.CreationTime), len(f.Content)
		out.CreationTime, out.Bytes, out.Sha256 = &created, &size, f.Sha256
		out.URL = "http://" + r.Host + cidFilesPath + strconv.FormatInt(f.ID, 10)
	}
	return out
}

// cidFileContent serves a file, one CID per line.
func (s *Server) cidFileContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("Id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	content, err := s.svc.CIDFileContent(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content)
}

func (s *Server) listCidSetEvents(r *http.Request, caller string) (int, any, error) {
	q := r.URL.Query()
	in := dict.ListCIDEventsInput{Participant: q.Get("Participant"), KeyType: dict.KeyType(q.Get("KeyType"))}
	var v []dict.Violation
	for _, p := range []struct {
		name string
		into *time.Time
	}{{"StartTime", &in.Start}, {"EndTime", &in.End}} {
		if raw := q.Get(p.name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				v = append(v, dict.Violation{Property: p.name, Value: raw, Reason: "Value must be a date-time"})
			}
			*p.into = t
		}
	}
	if raw := q.Get("Limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			v = append(v, dict.Violation{Property: "Limit", Value: raw, Reason: "Limit must be between 1 and 200"})
		}
		in.Limit = n
	}
	if len(v) > 0 {
		return 0, nil, &dict.Error{Kind: dict.KindBadRequest, Detail: "Request has invalid parameters", Violations: v}
	}
	page, err := s.svc.ListCIDEvents(r.Context(), caller, in)
	if err != nil {
		return 0, nil, err
	}
	resp := listCidSetEventsResponse{
		header: s.newHeader(r), HasMoreElements: page.HasMore, Participant: in.Participant, KeyType: string(in.KeyType),
		StartTime: isoTime(page.Start), EndTime: isoTime(page.End),
		SyncVerifierStart: page.VerifierStart, SyncVerifierEnd: page.VerifierEnd,
	}
	for _, ev := range page.Events {
		resp.CidSetEvents = append(resp.CidSetEvents, cidSetEventXML{Type: string(ev.Type), Cid: ev.CID, Timestamp: isoTime(ev.Timestamp)})
	}
	return http.StatusOK, resp, nil
}

func (s *Server) getEntryByCid(r *http.Request, caller string) (int, any, error) {
	e, err := s.svc.EntryByCID(r.Context(), caller, r.PathValue("Cid"))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, getEntryByCidResponse{header: s.newHeader(r), Cid: e.CID, Entry: entryToXML(e), RequestID: e.RequestID}, nil
}
