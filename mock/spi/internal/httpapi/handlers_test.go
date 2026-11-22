package httpapi_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pixfake/mock/spi/internal/httpapi"
	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/spi"
)

const (
	base = httpapi.BasePath
	psp  = "11111111"
)

type env struct {
	t     *testing.T
	url   string
	svc   *spi.Service
	store *memstore.Store
}

func newEnv(t *testing.T, pollWait time.Duration) *env {
	t.Helper()
	store := memstore.New()
	svc := spi.NewService(store, pollWait)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpapi.New(svc, log, nil).Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, url: srv.URL, svc: svc, store: store}
}

type reply struct {
	status int
	header http.Header
	body   string
}

func (e *env) do(method, path string, body []byte, headers map[string]string) reply {
	e.t.Helper()
	// A nil body must stay nil: an empty reader would make the client send Content-Length: 0.
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.url+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Setting Accept-Encoding by hand turns off the client's transparent gzip, so tests see the raw response.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header, string(raw)}
}

func xmlHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/xml; charset=utf-8"}
}

func (e *env) send(body string) reply {
	return e.do(http.MethodPost, base+"/in/"+psp+"/msgs", []byte(body), xmlHeaders())
}

func (e *env) deliver(ispb string, bodies ...string) []string {
	e.t.Helper()
	var ids []string
	for _, b := range bodies {
		id, err := e.svc.Deliver(context.Background(), ispb, []byte(b))
		if err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return buf.Bytes()
}

func assertProblem(t *testing.T, r reply, status int, slug string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d (body %s)", r.status, status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/problem+xml" {
		t.Fatalf("Content-Type = %q, want application/problem+xml", ct)
	}
	var p struct {
		Type  string `xml:"type"`
		Title string `xml:"title"`
	}
	if err := xml.Unmarshal([]byte(r.body), &p); err != nil {
		t.Fatalf("body is not XML: %v\n%s", err, r.body)
	}
	if want := "https://icom.pi.rsfn.net.br/api/v1/error/" + slug; p.Type != want {
		t.Fatalf("problem type = %q, want %q", p.Type, want)
	}
	if p.Title == "" {
		t.Fatal("problem has no title")
	}
}

// --- Input ---

func TestSendOneMessage(t *testing.T) {
	e := newEnv(t, 0)
	r := e.send("<msg>one</msg>")
	if r.status != http.StatusCreated {
		t.Fatalf("status = %d, body %s", r.status, r.body)
	}
	id := r.header.Get("PI-ResourceId")
	if len(id) != 32 {
		t.Fatalf("PI-ResourceId = %q, want 32 characters", id)
	}
	in := e.store.Inbound()
	if len(in) != 1 || string(in[0].Body) != "<msg>one</msg>" || in[0].ISPB != psp || in[0].ResourceID != id {
		t.Fatalf("stored = %+v", in)
	}
}

func TestSendTwiceDistinctIDs(t *testing.T) {
	e := newEnv(t, 0)
	a := e.send("<msg/>").header.Get("PI-ResourceId")
	b := e.send("<msg/>").header.Get("PI-ResourceId")
	if a == b {
		t.Fatalf("ResourceId repeated: %q", a)
	}
}

func TestSendGzip(t *testing.T) {
	e := newEnv(t, 0)
	h := xmlHeaders()
	h["Content-Encoding"] = "gzip"
	r := e.do(http.MethodPost, base+"/in/"+psp+"/msgs", gz(t, "<msg>zipped</msg>"), h)
	if r.status != http.StatusCreated {
		t.Fatalf("status = %d, body %s", r.status, r.body)
	}
	if got := string(e.store.Inbound()[0].Body); got != "<msg>zipped</msg>" {
		t.Fatalf("stored body = %q", got)
	}
}

func multipartBody(t *testing.T, parts ...string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		w, err := mw.CreatePart(map[string][]string{"Content-Type": {"application/xml; charset=utf-8"}})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, p)
	}
	_ = mw.Close()
	return buf.Bytes(), "multipart/mixed; boundary=" + mw.Boundary()
}

func TestSendMultipart(t *testing.T) {
	e := newEnv(t, 0)
	body, ct := multipartBody(t, "<m>1</m>", "<m>2</m>")
	r := e.do(http.MethodPost, base+"/in/"+psp+"/msgs", body, map[string]string{"Content-Type": ct})
	if r.status != http.StatusCreated {
		t.Fatalf("status = %d, body %s", r.status, r.body)
	}
	ids := strings.Split(r.header.Get("PI-ResourceId"), ",")
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("PI-ResourceId = %q, want two distinct ids", r.header.Get("PI-ResourceId"))
	}
	in := e.store.Inbound()
	if len(in) != 2 || string(in[0].Body) != "<m>1</m>" || string(in[1].Body) != "<m>2</m>" || in[1].ResourceID != ids[1] {
		t.Fatalf("stored = %+v", in)
	}
}

func TestSendMultipartLimit(t *testing.T) {
	e := newEnv(t, 0)
	tenParts := make([]string, 10)
	for i := range tenParts {
		tenParts[i] = fmt.Sprintf("<m>%d</m>", i)
	}
	body, ct := multipartBody(t, tenParts...)
	if r := e.do(http.MethodPost, base+"/in/"+psp+"/msgs", body, map[string]string{"Content-Type": ct}); r.status != http.StatusCreated {
		t.Fatalf("10 messages: status = %d", r.status)
	}
	body, ct = multipartBody(t, append(tenParts, "<m>11</m>")...)
	r := e.do(http.MethodPost, base+"/in/"+psp+"/msgs", body, map[string]string{"Content-Type": ct})
	assertProblem(t, r, http.StatusBadRequest, "too-many-messages")
	if n := len(e.store.Inbound()); n != 10 {
		t.Fatalf("stored %d messages; the rejected request must store none (10 from the first request)", n)
	}
}

func TestSendRejections(t *testing.T) {
	url := base + "/in/" + psp + "/msgs"
	tests := []struct {
		name    string
		path    string
		headers map[string]string
		body    []byte
		status  int
		slug    string
	}{
		{"wrong charset", url, map[string]string{"Content-Type": "application/xml; charset=utf-16"}, []byte("<m/>"), 400, "charset"},
		{"no charset", url, map[string]string{"Content-Type": "application/xml"}, []byte("<m/>"), 415, "media-type"},
		{"no content type", url, nil, []byte("<m/>"), 415, "media-type"},
		{"json", url, map[string]string{"Content-Type": "application/json"}, []byte("{}"), 415, "media-type"},
		{"unsupported encoding", url, map[string]string{"Content-Type": "application/xml; charset=utf-8", "Content-Encoding": "br"}, []byte("<m/>"), 415, "media-type"},
		{"broken gzip", url, map[string]string{"Content-Type": "application/xml; charset=utf-8", "Content-Encoding": "gzip"}, []byte("not gzip"), 400, "bad-request"},
		{"empty body", url, xmlHeaders(), []byte{}, 400, "no-messages"},
		{"invalid ispb", base + "/in/123/msgs", xmlHeaders(), []byte("<m/>"), 400, "ispb"},
		{"lowercase ispb", base + "/in/abcdefgh/msgs", xmlHeaders(), []byte("<m/>"), 400, "ispb"},
		{"multipart without boundary", url, map[string]string{"Content-Type": "multipart/mixed"}, []byte("x"), 400, "bad-request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, 0)
			r := e.do(http.MethodPost, tt.path, tt.body, tt.headers)
			assertProblem(t, r, tt.status, tt.slug)
			if n := len(e.store.Inbound()); n != 0 {
				t.Fatalf("stored %d messages; a rejected request must store none", n)
			}
		})
	}
}

func TestSendNoLength(t *testing.T) {
	e := newEnv(t, 0)
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprintf(conn, "POST %s/in/%s/msgs HTTP/1.1\r\nHost: x\r\nContent-Type: application/xml; charset=utf-8\r\nConnection: close\r\n\r\n", base, psp)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusLengthRequired {
		t.Fatalf("status = %d, want 411", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	e := newEnv(t, 0)
	r := e.do(http.MethodGet, base+"/in/"+psp+"/msgs", nil, nil)
	assertProblem(t, r, http.StatusMethodNotAllowed, "method-not-allowed")
	if r.header.Get("Allow") != "POST" {
		t.Fatalf("Allow = %q", r.header.Get("Allow"))
	}
	assertProblem(t, e.do(http.MethodPost, base+"/out/catalog", []byte("x"), nil), http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestUnknownPath(t *testing.T) {
	e := newEnv(t, 0)
	assertProblem(t, e.do(http.MethodGet, "/api/v1/nope", nil, nil), http.StatusNotFound, "not-found")
}

// --- Output ---

func TestStreamOrder(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	ids := e.deliver(psp, "<m>1</m>", "<m>2</m>")

	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil)
	if r.status != 200 || r.body != "<m>1</m>" || r.header.Get("PI-ResourceId") != ids[0] {
		t.Fatalf("first read = %d %q %q", r.status, r.body, r.header.Get("PI-ResourceId"))
	}
	if ct := r.header.Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	next := r.header.Get("PI-Pull-Next")
	first := next // the position after the first message
	if !strings.HasPrefix(next, base+"/out/"+psp+"/stream/") {
		t.Fatalf("PI-Pull-Next = %q", next)
	}

	r = e.do(http.MethodGet, next, nil, nil)
	if r.status != 200 || r.body != "<m>2</m>" || r.header.Get("PI-ResourceId") != ids[1] {
		t.Fatalf("second read = %d %q", r.status, r.body)
	}
	next = r.header.Get("PI-Pull-Next")

	r = e.do(http.MethodGet, next, nil, nil)
	if r.status != http.StatusNoContent || r.header.Get("PI-Pull-Next") == "" {
		t.Fatalf("third read = %d, PI-Pull-Next %q; want 204 with a next path", r.status, r.header.Get("PI-Pull-Next"))
	}
	next = r.header.Get("PI-Pull-Next")

	if r = e.do(http.MethodDelete, next, nil, nil); r.status != http.StatusOK {
		t.Fatalf("delete = %d", r.status)
	}
	// Closing again is harmless.
	if r = e.do(http.MethodDelete, next, nil, nil); r.status != http.StatusOK {
		t.Fatalf("second delete = %d, want 200", r.status)
	}
	// A token older than what was acknowledged is gone.
	assertProblem(t, e.do(http.MethodGet, first, nil, nil), http.StatusGone, "gone")
	assertProblem(t, e.do(http.MethodDelete, first, nil, nil), http.StatusGone, "gone")
}

func TestStreamRedelivery(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	ids := e.deliver(psp, "<m>1</m>", "<m>2</m>")

	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil)
	next := r.header.Get("PI-Pull-Next")
	// Read ended without DELETE: a new stream delivers the same message, with the same ResourceId.
	again := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil)
	if again.header.Get("PI-ResourceId") != ids[0] {
		t.Fatalf("redelivery ResourceId = %q, want %q", again.header.Get("PI-ResourceId"), ids[0])
	}

	// Closing the first stream acknowledges message 1: the next stream starts at message 2.
	if r := e.do(http.MethodDelete, next, nil, nil); r.status != 200 {
		t.Fatalf("delete = %d", r.status)
	}
	third := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil)
	if third.header.Get("PI-ResourceId") != ids[1] {
		t.Fatalf("after ack got %q, want message 2 (%q)", third.header.Get("PI-ResourceId"), ids[1])
	}
}

func TestStreamMultipart(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	ids := e.deliver(psp, "<m>1</m>", "<m>2</m>", "<m>3</m>")

	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, map[string]string{"Accept": "multipart/mixed"})
	if r.status != 200 {
		t.Fatalf("status = %d, body %s", r.status, r.body)
	}
	mt, params, err := mime.ParseMediaType(r.header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("Content-Type = %q", r.header.Get("Content-Type"))
	}
	mr := multipart.NewReader(strings.NewReader(r.body), params["boundary"])
	for i, want := range []string{"<m>1</m>", "<m>2</m>", "<m>3</m>"} {
		p, err := mr.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		b, _ := io.ReadAll(p)
		if string(b) != want || p.Header.Get("PI-ResourceId") != ids[i] {
			t.Fatalf("part %d = %q %q, want %q %q", i, b, p.Header.Get("PI-ResourceId"), want, ids[i])
		}
	}
	if r.header.Get("PI-Pull-Next") == "" {
		t.Fatal("no PI-Pull-Next")
	}
}

func TestStreamMultipartLimit(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	for i := 0; i < 12; i++ {
		e.deliver(psp, fmt.Sprintf("<m>%d</m>", i))
	}
	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, map[string]string{"Accept": "multipart/mixed"})
	_, params, _ := mime.ParseMediaType(r.header.Get("Content-Type"))
	mr := multipart.NewReader(strings.NewReader(r.body), params["boundary"])
	n := 0
	for {
		if _, err := mr.NextPart(); err != nil {
			break
		}
		n++
	}
	if n != 10 {
		t.Fatalf("parts = %d, want 10", n)
	}
}

func TestStreamGzip(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	e.deliver(psp, "<m>zipped</m>")
	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, map[string]string{"Accept-Encoding": "gzip"})
	if r.header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", r.header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(strings.NewReader(r.body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if string(b) != "<m>zipped</m>" {
		t.Fatalf("body = %q", b)
	}
}

func TestStreamLongPoll(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.deliver(psp, "<m>late</m>")
	}()
	start := time.Now()
	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil)
	if r.status != 200 || r.body != "<m>late</m>" {
		t.Fatalf("read = %d %q", r.status, r.body)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("read took %v; it should return as soon as the message arrives", d)
	}
}

func TestStream204(t *testing.T) {
	e := newEnv(t, 150*time.Millisecond)
	start := time.Now()
	r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil)
	if r.status != http.StatusNoContent {
		t.Fatalf("status = %d", r.status)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Fatalf("answered after %v; it should wait for messages first", d)
	}
}

func TestStreamPerParticipant(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	e.deliver("22222222", "<m>for 2</m>")
	if r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, nil); r.status != http.StatusNoContent {
		t.Fatalf("participant 1 got status %d, want 204", r.status)
	}
	if r := e.do(http.MethodGet, base+"/out/22222222/stream/start", nil, nil); r.status != 200 || r.body != "<m>for 2</m>" {
		t.Fatalf("participant 2 got %d %q", r.status, r.body)
	}
}

func TestStreamBadToken(t *testing.T) {
	e := newEnv(t, 0)
	for _, token := range []string{"nope", "-1", "zz", "1.5"} {
		assertProblem(t, e.do(http.MethodGet, base+"/out/"+psp+"/stream/"+token, nil, nil), http.StatusGone, "gone")
		assertProblem(t, e.do(http.MethodDelete, base+"/out/"+psp+"/stream/"+token, nil, nil), http.StatusGone, "gone")
	}
}

func TestStreamAcceptHeader(t *testing.T) {
	e := newEnv(t, 0)
	assertProblem(t, e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, map[string]string{"Accept": "application/json"}),
		http.StatusNotAcceptable, "not-acceptable")
	for _, ok := range []string{"application/xml", "*/*", "multipart/mixed", "application/xml, multipart/mixed"} {
		if r := e.do(http.MethodGet, base+"/out/"+psp+"/stream/start", nil, map[string]string{"Accept": ok}); r.status != http.StatusNoContent {
			t.Errorf("Accept %q: status = %d, want 204", ok, r.status)
		}
	}
	assertProblem(t, e.do(http.MethodGet, base+"/out/BAD/stream/start", nil, nil), http.StatusBadRequest, "ispb")
}

// --- Catalog ---

func TestCatalogs(t *testing.T) {
	e := newEnv(t, 0)
	for _, tt := range []struct {
		path string
		want []string
	}{
		{base + "/in/catalog", []string{"pacs.008.spi.1.16", "pacs.002.spi.1.18", "pacs.004.spi.1.5", "trck.002.spi.1.1"}},
		{base + "/out/catalog", []string{"pacs.008.spi.1.16", "pacs.002.spi.1.18", "admi.002.spi.1.5"}},
	} {
		r := e.do(http.MethodGet, tt.path, nil, nil)
		if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "application/xml") {
			t.Fatalf("%s: status %d, Content-Type %q", tt.path, r.status, r.header.Get("Content-Type"))
		}
		var c struct {
			Message []string `xml:"Message"`
		}
		if err := xml.Unmarshal([]byte(r.body), &c); err != nil {
			t.Fatalf("%s: %v", tt.path, err)
		}
		have := map[string]bool{}
		for _, m := range c.Message {
			have[m] = true
		}
		for _, w := range tt.want {
			if !have[w] {
				t.Errorf("%s: missing %s in %v", tt.path, w, c.Message)
			}
		}
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t, 0)
	if r := e.do(http.MethodGet, "/healthz", nil, nil); r.status != 200 || r.body != "ok" {
		t.Fatalf("healthz = %d %q", r.status, r.body)
	}
}

func TestUnknownParticipant(t *testing.T) {
	store := memstore.New()
	svc := spi.NewService(store, 0)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	known := func(ispb string) bool { return ispb == psp }
	srv := httptest.NewServer(httpapi.New(svc, log, nil).WithParticipants(known).Handler())
	defer srv.Close()
	e := &env{t: t, url: srv.URL, svc: svc, store: store}

	other := "22222222"
	assertProblem(t, e.do(http.MethodPost, base+"/in/"+other+"/msgs", []byte("<m/>"), xmlHeaders()), http.StatusForbidden, "forbidden")
	assertProblem(t, e.do(http.MethodGet, base+"/out/"+other+"/stream/start", nil, nil), http.StatusForbidden, "forbidden")
	assertProblem(t, e.do(http.MethodGet, base+"/out/"+other+"/stream/0", nil, nil), http.StatusForbidden, "forbidden")
	assertProblem(t, e.do(http.MethodDelete, base+"/out/"+other+"/stream/0", nil, nil), http.StatusForbidden, "forbidden")
	if n := len(store.Inbound()); n != 0 {
		t.Fatalf("stored %d messages from a participant that is not registered", n)
	}

	// A registered participant works, and a malformed ISPB is still a 400, not a 403.
	if r := e.do(http.MethodPost, base+"/in/"+psp+"/msgs", []byte("<m/>"), xmlHeaders()); r.status != http.StatusCreated {
		t.Fatalf("registered participant = %d", r.status)
	}
	assertProblem(t, e.do(http.MethodPost, base+"/in/abc/msgs", []byte("<m/>"), xmlHeaders()), http.StatusBadRequest, "ispb")
}
