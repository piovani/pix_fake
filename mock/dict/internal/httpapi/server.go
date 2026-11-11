// Package httpapi exposes the DICT service over HTTP/XML, in the Bacen spec format.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"pixfake/mock/dict/internal/dict"
)

const (
	// BasePath is the API prefix, same as the real URL (…/api/v2).
	BasePath = "/api/v2"
	// ParticipantHeader identifies the calling institution. It replaces mTLS.
	ParticipantHeader = "X-Participant-ISPB"

	maxBodyBytes = 1 << 20
)

type Server struct {
	svc    *dict.Service
	log    *slog.Logger
	health func(context.Context) error
	now    func() time.Time
}

// New creates the server. health is called on GET /healthz (it may be nil).
func New(svc *dict.Service, log *slog.Logger, health func(context.Context) error) *Server {
	return &Server{svc: svc, log: log, health: health, now: time.Now}
}

// handlerFunc returns the status and success body, or an error.
type handlerFunc func(r *http.Request, caller string) (status int, body any, err error)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("POST "+BasePath+"/entries", s.api(s.createEntry))
	mux.Handle("GET "+BasePath+"/entries/{Key}", s.api(s.getEntry))
	mux.Handle("PUT "+BasePath+"/entries/{Key}", s.api(s.updateEntry))
	mux.Handle("POST "+BasePath+"/entries/{Key}/delete", s.api(s.deleteEntry))
	mux.Handle("POST "+BasePath+"/keys/check", s.api(s.checkKeys))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, &dict.Error{Kind: dict.KindNotFound, Detail: "Resource does not exist"})
	})
	return s.recoverer(s.withCorrelationID(mux))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if s.health != nil {
		if err := s.health(r.Context()); err != nil {
			s.log.Error("health check failed", "error", err)
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	_, _ = io.WriteString(w, "ok")
}

// api requires the institution identification header and translates the result or error into XML.
func (s *Server) api(fn handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := r.Header.Get(ParticipantHeader)
		if !dict.ValidISPB(caller) {
			s.writeError(w, r, &dict.Error{
				Kind:   dict.KindForbidden,
				Detail: "Missing or invalid " + ParticipantHeader + " header",
			})
			return
		}
		status, body, err := fn(r, caller)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		s.writeXML(w, status, "application/xml", body)
	})
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var de *dict.Error
	if !errors.As(err, &de) {
		s.log.Error("unexpected error", "method", r.Method, "path", r.URL.Path, "error", err)
		de = &dict.Error{Kind: kindServiceUnavailable, Detail: "Unexpected error, try again later"}
		w.Header().Set("Retry-After", "1")
	}
	status, p := newProblem(de, correlationID(r.Context()))
	s.writeXML(w, status, "application/problem+xml", p)
}

func (s *Server) writeXML(w http.ResponseWriter, status int, contentType string, body any) {
	out, err := xml.MarshalIndent(body, "", "    ")
	if err != nil {
		s.log.Error("encode response", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header)
	_, _ = w.Write(out)
}

func decodeXML(r *http.Request, v any) error {
	if err := xml.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(v); err != nil {
		return &dict.Error{Kind: dict.KindBadRequest, Detail: "Could not parse request body"}
	}
	return nil
}

// --- Middlewares ---

type ctxKey struct{}

func correlationID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

func (s *Server) withCorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [16]byte
		_, _ = rand.Read(b[:])
		ctx := context.WithValue(r.Context(), ctxKey{}, hex.EncodeToString(b[:]))
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(start), "correlation_id", correlationID(ctx))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "panic", rec, "path", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
