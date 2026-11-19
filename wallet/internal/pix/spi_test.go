package pix

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

func newTestSPI(t *testing.T, h http.HandlerFunc) *SPIClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSPIClient(srv.URL+"/api/v1", "99000001")
}

func TestSendOneMessage(t *testing.T) {
	c := newTestSPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/in/99000001/msgs" ||
			r.Header.Get("Content-Type") != "application/xml; charset=utf-8" || string(body) != "<a/>" {
			t.Errorf("request: %s %s %q %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"), body)
		}
		w.Header().Set("PI-ResourceId", "res1")
		w.WriteHeader(http.StatusCreated)
	})
	ids, err := c.Send(context.Background(), []byte("<a/>"))
	if err != nil || len(ids) != 1 || ids[0] != "res1" {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

func TestSendSeveralMessagesAsMultipart(t *testing.T) {
	c := newTestSPI(t, func(w http.ResponseWriter, r *http.Request) {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/mixed" {
			t.Fatalf("content type %q", r.Header.Get("Content-Type"))
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var got []string
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			got = append(got, string(b))
		}
		if strings.Join(got, ",") != "<a/>,<b/>" {
			t.Errorf("parts = %v", got)
		}
		w.Header().Set("PI-ResourceId", "r1,r2")
		w.WriteHeader(http.StatusCreated)
	})
	ids, err := c.Send(context.Background(), []byte("<a/>"), []byte("<b/>"))
	if err != nil || len(ids) != 2 || ids[1] != "r2" {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

func TestSendValidatesCount(t *testing.T) {
	c := newTestSPI(t, func(http.ResponseWriter, *http.Request) { t.Error("the SPI must not be called") })
	if _, err := c.Send(context.Background()); !errors.Is(err, ErrBadRequest) {
		t.Errorf("none: %v", err)
	}
	if _, err := c.Send(context.Background(), make([][]byte, 11)...); !errors.Is(err, ErrBadRequest) {
		t.Errorf("eleven: %v", err)
	}
}

func TestPullSingleMessage(t *testing.T) {
	var paths []string
	c := newTestSPI(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Accept") != "multipart/mixed" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("PI-Pull-Next", "/api/v1/out/99000001/stream/t2")
		if len(paths) == 1 {
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("PI-ResourceId", "res1")
			_, _ = io.WriteString(w, "<msg/>")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	msgs, next, err := c.Pull(context.Background(), "")
	if err != nil || len(msgs) != 1 || msgs[0].ResourceID != "res1" || string(msgs[0].Body) != "<msg/>" || next != "/api/v1/out/99000001/stream/t2" {
		t.Fatalf("msgs = %+v, next = %q, err = %v", msgs, next, err)
	}
	msgs, next2, err := c.Pull(context.Background(), next)
	if err != nil || len(msgs) != 0 || next2 == "" {
		t.Fatalf("204: msgs = %+v, next = %q, err = %v", msgs, next2, err)
	}
	if paths[0] != "/api/v1/out/99000001/stream/start" || paths[1] != "/api/v1/out/99000001/stream/t2" {
		t.Errorf("paths = %v", paths)
	}
}

func TestPullMultipart(t *testing.T) {
	c := newTestSPI(t, func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, m := range [][2]string{{"r1", "<a/>"}, {"r2", "<b/>"}} {
			p, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/xml"}, "PI-ResourceId": {m[0]}})
			_, _ = io.WriteString(p, m[1])
		}
		_ = mw.Close()
		w.Header().Set("PI-Pull-Next", "/api/v1/out/99000001/stream/n")
		w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		_, _ = w.Write(buf.Bytes())
	})
	msgs, _, err := c.Pull(context.Background(), "")
	if err != nil || len(msgs) != 2 || msgs[0].ResourceID != "r1" || string(msgs[1].Body) != "<b/>" {
		t.Fatalf("msgs = %+v, err = %v", msgs, err)
	}
}

func TestAckAndGone(t *testing.T) {
	c := newTestSPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method %s", r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "/old") {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	ctx := context.Background()
	if err := c.Ack(ctx, "/api/v1/out/99000001/stream/ok"); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := c.Ack(ctx, "/api/v1/out/99000001/stream/old"); !errors.Is(err, ErrStreamGone) {
		t.Fatalf("410: %v", err)
	}
	if err := c.Ack(ctx, ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("empty: %v", err)
	}
}

func TestSPIErrorMapping(t *testing.T) {
	tests := []struct {
		status int
		is     error
		retry  bool
	}{
		{400, ErrBadRequest, false}, {403, ErrForbidden, false}, {429, ErrRateLimited, true}, {503, ErrUnavailable, true},
	}
	for _, tt := range tests {
		c := newTestSPI(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "4")
			w.Header().Set("Content-Type", "application/problem+xml")
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, problem("SomeKind", "t", tt.status, `<detail>why</detail>`))
		})
		_, err := c.Send(context.Background(), []byte("<a/>"))
		var se *SPIError
		if !errors.As(err, &se) || se.Kind != "SomeKind" || se.Detail != "why" || se.RetryAfter.Seconds() != 4 {
			t.Fatalf("status %d: %#v", tt.status, err)
		}
		if !errors.Is(err, tt.is) || Retryable(err) != tt.retry {
			t.Errorf("status %d: is %v = %v, retryable = %v", tt.status, tt.is, errors.Is(err, tt.is), Retryable(err))
		}
	}
}

func TestSPINetworkFailureIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := NewSPIClient(url+"/api/v1", "99000001")
	if _, err := c.Send(context.Background(), []byte("<a/>")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("send: %v", err)
	}
	if _, _, err := c.Pull(context.Background(), ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pull: %v", err)
	}
}
