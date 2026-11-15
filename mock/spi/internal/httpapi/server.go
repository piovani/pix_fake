// Package httpapi exposes the SPI message exchange (ICOM) over HTTP/XML, in the Bacen
// format: send on /api/v1/in/{ispb}/msgs, read from /api/v1/out/{ispb}/stream/....
package httpapi

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"pixfake/mock/spi/internal/spi"
)

const (
	// BasePath is the API prefix, same as the real URL (…/api/v1).
	BasePath = "/api/v1"

	xmlType = "application/xml; charset=utf-8"
	// maxBodyBytes limits a request after decompression.
	maxBodyBytes = 10 << 20
)

type Server struct {
	svc    *spi.Service
	log    *slog.Logger
	health func(context.Context) error
	now    func() time.Time
}

// New creates the server. health is called on GET /healthz (it may be nil).
func New(svc *spi.Service, log *slog.Logger, health func(context.Context) error) *Server {
	return &Server{svc: svc, log: log, health: health, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	// Patterns carry no method: the handlers answer 405 in the API's problem format.
	mux.Handle(BasePath+"/in/{ispb}/msgs", s.api(s.send, http.MethodPost))
	mux.Handle(BasePath+"/out/{ispb}/stream/start", s.api(s.startStream, http.MethodGet))
	mux.Handle(BasePath+"/out/{ispb}/stream/{token}", s.api(s.stream, http.MethodGet, http.MethodDelete))
	mux.Handle(BasePath+"/in/catalog", s.api(s.catalog(inCatalog), http.MethodGet))
	mux.Handle(BasePath+"/out/catalog", s.api(s.catalog(outCatalog), http.MethodGet))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.writeError(w, errNotFound) })
	return s.recoverer(s.logRequests(mux))
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

// handlerFunc writes its own success response and returns an error for the problem response.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// api checks the HTTP method and turns returned errors into problem+xml responses.
func (s *Server) api(fn handlerFunc, methods ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(methods, r.Method) {
			w.Header().Set("Allow", strings.Join(methods, ", "))
			s.writeError(w, errMethod)
			return
		}
		if err := fn(w, r); err != nil {
			s.writeError(w, err)
		}
	})
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		s.log.Error("unexpected error", "error", err)
		ae = errUnavailable
		w.Header().Set("Retry-After", "1")
	}
	out, _ := xml.MarshalIndent(newProblem(ae), "", "  ")
	w.Header().Set("Content-Type", "application/problem+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(ae.status)
	_, _ = io.WriteString(w, xml.Header)
	_, _ = w.Write(out)
}

// --- Middlewares ---

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(start))
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
