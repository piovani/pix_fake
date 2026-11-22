package pix

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The clients may only know what the configuration tells them: a base URL (host and path prefix) and the participant's
// ISPB. Behind another host, another prefix and another ISPB, everything must still work, and nothing may touch a mock path.

const (
	otherISPB   = "12345678"
	otherPrefix = "/bacen/producao/spi/v7"
)

type seen struct {
	mu    sync.Mutex
	paths []string
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, r.Method+" "+r.URL.EscapedPath())
}

func (s *seen) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func TestTheSPIClientNeedsOnlyTheBaseURLAndTheISPB(t *testing.T) {
	var got seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.add(r)
		switch {
		case r.Method == http.MethodPost:
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("PI-ResourceId", "res1")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet:
			w.Header().Set("PI-Pull-Next", otherPrefix+"/out/"+otherISPB+"/stream/t2")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewSPIClient(srv.URL+otherPrefix, otherISPB)
	ctx := context.Background()
	if _, err := c.Send(ctx, []byte("<a/>")); err != nil {
		t.Fatal(err)
	}
	_, next, err := c.Pull(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, next, err = c.Pull(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack(ctx, next); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"POST " + otherPrefix + "/in/" + otherISPB + "/msgs",
		"GET " + otherPrefix + "/out/" + otherISPB + "/stream/start",
		"GET " + otherPrefix + "/out/" + otherISPB + "/stream/t2",
		"DELETE " + otherPrefix + "/out/" + otherISPB + "/stream/t2",
	}
	if paths := got.all(); strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(paths, "\n"), strings.Join(want, "\n"))
	}
}

func TestTheDICTClientNeedsOnlyTheBaseURLAndTheISPB(t *testing.T) {
	var got seen
	var ispb string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.add(r)
		ispb = r.Header.Get("X-Participant-ISPB") + "/" + r.Header.Get("PI-RequestingParticipant")
		writeXML(w, 200, entryAnswer)
	}))
	t.Cleanup(srv.Close)

	c := NewDictClient(srv.URL+"/bacen/producao/dict/v9", otherISPB)
	if _, err := c.GetEntry(context.Background(), GetEntryParams{
		Key: "+5521999990003", PayerID: "52998224725", EndToEndID: "E" + otherISPB + "202611191200abcdefghijk",
	}); err != nil {
		t.Fatal(err)
	}
	if paths := got.all(); len(paths) != 1 || paths[0] != "GET /bacen/producao/dict/v9/entries/%2B5521999990003" {
		t.Fatalf("requests: %v", paths)
	}
	if ispb != otherISPB+"/"+otherISPB {
		t.Fatalf("the participant must come from the configuration: %q", ispb)
	}
}

func TestNoRequestTouchesAMockPath(t *testing.T) {
	var got seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.add(r)
		w.Header().Set("PI-ResourceId", "res1")
		w.Header().Set("PI-Pull-Next", "/out/x/stream/t")
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	spi := NewSPIClient(srv.URL, otherISPB)
	_, _ = spi.Send(ctx, []byte("<a/>"))
	_, _, _ = spi.Pull(ctx, "")
	_ = spi.Ack(ctx, "/out/x/stream/t")
	dict := NewDictClient(srv.URL, otherISPB)
	_, _ = dict.GetEntry(ctx, GetEntryParams{Key: "a@b.co", PayerID: "52998224725", EndToEndID: "E" + otherISPB + "202611191200abcdefghijk"})

	paths := got.all()
	if len(paths) < 4 {
		t.Fatalf("only %d requests: %v", len(paths), paths)
	}
	for _, p := range paths {
		if strings.Contains(p, "/mock") {
			t.Errorf("a client called %s", p)
		}
	}
}
