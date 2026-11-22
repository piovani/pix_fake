package fault_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pixfake/mock/dict/internal/fault"
)

func serve(t *testing.T, s *fault.Set) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return s.Middleware(next, []string{"/mock/", "/healthz"}, func(w http.ResponseWriter, _ *http.Request, status int) { w.WriteHeader(status) })
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestNoRulesNoFaults(t *testing.T) {
	if got := get(serve(t, &fault.Set{}), "GET", "/api/v2/x").Code; got != 200 {
		t.Fatalf("status = %d", got)
	}
}

func TestStatusRetryAfter(t *testing.T) {
	var s fault.Set
	if _, err := s.Add(fault.Rule{Status: 429, RetryAfter: 7, Count: 2}); err != nil {
		t.Fatal(err)
	}
	h := serve(t, &s)
	for i := 0; i < 2; i++ {
		rec := get(h, "POST", "/api/v2/in/11111111/msgs")
		if rec.Code != 429 || rec.Header().Get("Retry-After") != "7" {
			t.Fatalf("request %d = %d, Retry-After %q; want 429 and 7", i, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	if got := get(h, "POST", "/api/v2/in/11111111/msgs").Code; got != 200 {
		t.Fatalf("third request = %d; the rule was for two requests", got)
	}
	if len(s.List()) != 0 {
		t.Fatalf("an exhausted rule stays: %+v", s.List())
	}
}

func TestRuleWithoutCount(t *testing.T) {
	var s fault.Set
	id, _ := s.Add(fault.Rule{Status: 503})
	h := serve(t, &s)
	for i := 0; i < 5; i++ {
		if got := get(h, "GET", "/api/v2/x").Code; got != 503 {
			t.Fatalf("request %d = %d", i, got)
		}
	}
	if !s.Remove(id) || s.Remove(id) {
		t.Fatal("Remove must report true once and then false")
	}
	if got := get(h, "GET", "/api/v2/x").Code; got != 200 {
		t.Fatalf("after removing = %d", got)
	}
}

func TestMatch(t *testing.T) {
	var s fault.Set
	_, _ = s.Add(fault.Rule{Method: "POST", PathPrefix: "/api/v2/in/", Status: 503})
	h := serve(t, &s)
	if got := get(h, "POST", "/api/v2/in/11111111/msgs").Code; got != 503 {
		t.Fatalf("matching request = %d", got)
	}
	if got := get(h, "GET", "/api/v2/in/catalog").Code; got != 200 {
		t.Fatalf("other method = %d", got)
	}
	if got := get(h, "POST", "/api/v2/out/x").Code; got != 200 {
		t.Fatalf("other path = %d", got)
	}
}

func TestControlExempt(t *testing.T) {
	var s fault.Set
	_, _ = s.Add(fault.Rule{Status: 503})
	h := serve(t, &s)
	for _, path := range []string{"/mock/faults", "/healthz"} {
		if got := get(h, "GET", path).Code; got != 200 {
			t.Errorf("%s = %d", path, got)
		}
	}
}

func TestDelayOnly(t *testing.T) {
	var s fault.Set
	_, _ = s.Add(fault.Rule{DelayMS: 80, Count: 1})
	h := serve(t, &s)
	start := time.Now()
	if got := get(h, "GET", "/api/v2/x").Code; got != 200 {
		t.Fatalf("status = %d; a delay-only rule still passes the request on", got)
	}
	if d := time.Since(start); d < 70*time.Millisecond {
		t.Fatalf("answered after %v; want at least 80ms", d)
	}
}

func TestValidate(t *testing.T) {
	bad := []fault.Rule{
		{}, {Status: 500}, {Status: 429, DelayMS: -1}, {DelayMS: 70000}, {Status: 503, RetryAfter: -1}, {Status: 503, Count: -1},
		{Status: 503, Method: "get"}, {Status: 503, PathPrefix: "api"}, {Status: 503, PathPrefix: "/mock/faults"},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v should be refused", r)
		}
	}
	for _, r := range []fault.Rule{{Status: 429, RetryAfter: 1}, {Status: 503, Count: 3, Method: "GET", PathPrefix: "/api"}, {DelayMS: 500}} {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}
