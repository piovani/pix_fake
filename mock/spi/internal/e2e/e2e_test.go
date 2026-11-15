// Package e2e tests the complete HTTP API on a real Postgres.
package e2e_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pixfake/mock/spi/internal/httpapi"
	"pixfake/mock/spi/internal/postgres"
	"pixfake/mock/spi/internal/postgres/pgtest"
	"pixfake/mock/spi/internal/spi"
)

const base = httpapi.BasePath

// newServer starts the real API (handlers + service + Postgres) in an httptest.Server.
// Without SPI_TEST_DATABASE_URL the test is skipped (see pgtest).
func newServer(t *testing.T) (*httptest.Server, *spi.Service) {
	t.Helper()
	pool := pgtest.NewPool(t)
	svc := spi.NewService(postgres.NewRepository(pool), 50*time.Millisecond)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpapi.New(svc, log, pool.Ping).Handler())
	t.Cleanup(srv.Close)
	return srv, svc
}

func call(t *testing.T, method, url string, body []byte, headers map[string]string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestSendThenReadFlow(t *testing.T) {
	srv, svc := newServer(t)

	// A participant sends a message: it is stored and gets a ResourceId.
	resp, _ := call(t, http.MethodPost, srv.URL+base+"/in/11111111/msgs", []byte("<m>in</m>"),
		map[string]string{"Content-Type": "application/xml; charset=utf-8"})
	if resp.StatusCode != http.StatusCreated || len(resp.Header.Get("PI-ResourceId")) != 32 {
		t.Fatalf("send = %d, PI-ResourceId %q", resp.StatusCode, resp.Header.Get("PI-ResourceId"))
	}

	// The SPI queues a message for a participant, which reads it, closes the stream and does not get it again.
	id, err := svc.Deliver(context.Background(), "22222222", []byte("<m>out</m>"))
	if err != nil {
		t.Fatal(err)
	}
	resp, body := call(t, http.MethodGet, srv.URL+base+"/out/22222222/stream/start", nil, nil)
	if resp.StatusCode != 200 || body != "<m>out</m>" || resp.Header.Get("PI-ResourceId") != id {
		t.Fatalf("read = %d %q %q", resp.StatusCode, body, resp.Header.Get("PI-ResourceId"))
	}
	next := resp.Header.Get("PI-Pull-Next")
	if resp, _ = call(t, http.MethodDelete, srv.URL+next, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	resp, _ = call(t, http.MethodGet, srv.URL+base+"/out/22222222/stream/start", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("read after ack = %d, want 204", resp.StatusCode)
	}
	if resp, _ = call(t, http.MethodGet, srv.URL+next, nil, nil); resp.StatusCode != http.StatusGone {
		t.Fatalf("closed stream = %d, want 410", resp.StatusCode)
	}
}

func TestHealthzChecksDatabase(t *testing.T) {
	srv, _ := newServer(t)
	if resp, body := call(t, http.MethodGet, srv.URL+"/healthz", nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}
}
