package pix

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const problemNS = `<problem xmlns="urn:ietf:rfc:7807">`

func newTestClient(t *testing.T, h http.HandlerFunc) *DictClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewDictClient(srv.URL+"/api/v2", "99000001")
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

const entryAnswer = `<GetEntryResponse><ResponseTime>2026-11-19T12:00:00.000Z</ResponseTime><CorrelationId>abc</CorrelationId>
<Entry><Key>+5521999990003</Key><KeyType>PHONE</KeyType>
<Account><Participant>99000002</Participant><Branch>0001</Branch><AccountNumber>00456789</AccountNumber><AccountType>CACC</AccountType><OpeningDate>2020-01-01T03:00:00.000Z</OpeningDate></Account>
<Owner><Type>NATURAL_PERSON</Type><TaxIdNumber>39053344705</TaxIdNumber><Name>Carlos Mendes</Name></Owner>
<CreationDate>2026-01-02T10:00:00.000Z</CreationDate><KeyOwnershipDate>2026-01-02T10:00:00.000Z</KeyOwnershipDate></Entry></GetEntryResponse>`

func TestGetEntry(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v2/entries/%2B5521999990003" {
			t.Errorf("request: %s %s", r.Method, r.URL.EscapedPath())
		}
		for k, want := range map[string]string{
			"X-Participant-ISPB":       "99000001",
			"PI-RequestingParticipant": "99000001",
			"PI-PayerId":               "52998224725",
			"PI-EndToEndId":            "E99000001202611191200abcdefghijk",
		} {
			if got := r.Header.Get(k); got != want {
				t.Errorf("header %s = %q, want %q", k, got, want)
			}
		}
		writeXML(w, 200, entryAnswer)
	})

	e, err := c.GetEntry(context.Background(), GetEntryParams{
		Key: "+5521999990003", PayerID: "52998224725", EndToEndID: "E99000001202611191200abcdefghijk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != "+5521999990003" || e.KeyType != KeyPhone || e.Account.Participant != "99000002" ||
		e.Account.AccountNumber != "00456789" || e.Account.AccountType != AccountChecking ||
		e.Owner.Name != "Carlos Mendes" || e.Owner.Type != NaturalPerson {
		t.Fatalf("entry: %+v", e)
	}
	if want := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC); !e.CreationDate.Equal(want) {
		t.Fatalf("CreationDate = %v, want %v", e.CreationDate, want)
	}
}

func TestCreateEntry(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req createEntryRequestXML
		if err := xml.Unmarshal(raw, &req); err != nil {
			t.Fatalf("body is not the expected XML: %v\n%s", err, raw)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/entries" || r.Header.Get("Content-Type") != "application/xml" {
			t.Errorf("request: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		if req.RequestID != "11111111-1111-4111-8111-111111111111" || req.Reason != "USER_REQUESTED" ||
			req.Entry.Key != "" || req.Entry.KeyType != "EVP" || req.Entry.Account.Participant != "99000001" ||
			!strings.Contains(string(raw), "<OpeningDate>2020-01-01T03:00:00.000Z</OpeningDate>") {
			t.Errorf("request body: %+v\n%s", req, raw)
		}
		writeXML(w, 201, strings.Replace(strings.Replace(entryAnswer, "GetEntryResponse", "CreateEntryResponse", 2),
			"<Key>+5521999990003</Key><KeyType>PHONE</KeyType>", "<Key>8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01</Key><KeyType>EVP</KeyType>", 1))
	})

	e, err := c.CreateEntry(context.Background(), Entry{
		KeyType: KeyEVP,
		Account: Account{Participant: "99000001", Branch: "0001", AccountNumber: "00123456", AccountType: AccountChecking,
			OpeningDate: time.Date(2020, 1, 1, 3, 0, 0, 0, time.UTC)},
		Owner: Owner{Type: NaturalPerson, TaxIDNumber: "52998224725", Name: "Ana Souza"},
	}, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01" {
		t.Fatalf("the generated key was not returned: %+v", e)
	}
}

func TestUpdateDeleteCheck(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/v2/entries/52998224725":
			if !strings.Contains(string(raw), "<Reason>USER_REQUESTED</Reason>") || strings.Contains(string(raw), "<Owner>") {
				t.Errorf("update body: %s", raw)
			}
			writeXML(w, 200, strings.Replace(entryAnswer, "GetEntryResponse", "UpdateEntryResponse", 2))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/entries/52998224725/delete":
			if !strings.Contains(string(raw), "<Participant>99000001</Participant>") {
				t.Errorf("delete body: %s", raw)
			}
			writeXML(w, 200, `<DeleteEntryResponse><Key>52998224725</Key></DeleteEntryResponse>`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/keys/check":
			writeXML(w, 200, `<CheckKeysResponse><Keys><Key hasEntry="true">a</Key><Key hasEntry="false">b</Key></Keys></CheckKeysResponse>`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()

	acc := Account{Participant: "99000001", AccountNumber: "00123456", AccountType: AccountSavings, OpeningDate: time.Now()}
	if _, err := c.UpdateEntry(ctx, UpdateEntryParams{Key: "52998224725", Account: &acc}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := c.DeleteEntry(ctx, "52998224725", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	st, err := c.CheckKeys(ctx, []string{"a", "b"})
	if err != nil || len(st) != 2 || !st[0].HasEntry || st[1].HasEntry || st[1].Key != "b" {
		t.Fatalf("check: %+v, %v", st, err)
	}
}

func TestDictValidation(t *testing.T) {
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("the DICT must not be called") })
	ctx := context.Background()

	if _, err := c.GetEntry(ctx, GetEntryParams{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("GetEntry without key: %v", err)
	}
	if _, err := c.CreateEntry(ctx, Entry{}, ""); !errors.Is(err, ErrBadRequest) {
		t.Errorf("CreateEntry without request id: %v", err)
	}
	if _, err := c.CheckKeys(ctx, nil); !errors.Is(err, ErrBadRequest) {
		t.Errorf("CheckKeys empty: %v", err)
	}
	if _, err := c.CheckKeys(ctx, make([]string, 201)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("CheckKeys 201: %v", err)
	}
}

func problem(kind, title string, status int, extra string) string {
	return `<?xml version="1.0"?>` + problemNS + `<type>https://dict.pi.rsfn.net.br/api/v2/error/` + kind + `</type><title>` +
		title + `</title><status>` + strconv.Itoa(status) + `</status><correlationId>c0ffee</correlationId>` + extra + `</problem>`
}

func TestDictErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		is         []error
		isNot      []error
		retryable  bool
	}{
		{"not found", 404, problem("NotFound", "Not found", 404, ""), "", []error{ErrNotFound}, []error{ErrConflict, ErrBookTransfer}, false},
		{"conflict", 409, problem("Conflict", "Conflict", 409, ""), "", []error{ErrConflict}, []error{ErrNotFound}, false},
		{"book transfer", 400, problem("EntryCannotBeQueriedForBookTransfer", "x", 400, ""), "", []error{ErrBookTransfer, ErrBadRequest}, []error{ErrNotFound}, false},
		{"request id reused", 400, problem("RequestIdAlreadyUsed", "x", 400, ""), "", []error{ErrRequestIDAlreadyUsed, ErrBadRequest}, []error{ErrBookTransfer}, false},
		{"forbidden", 403, problem("Forbidden", "Forbidden", 403, ""), "", []error{ErrForbidden}, []error{ErrUnavailable}, false},
		{"rate limited", 429, problem("TooManyRequests", "x", 429, ""), "7", []error{ErrRateLimited}, []error{ErrUnavailable}, true},
		{"unavailable", 503, problem("ServiceUnavailable", "x", 503, ""), "3", []error{ErrUnavailable}, []error{ErrRateLimited}, true},
		{"not a problem", 502, "<html>bad gateway</html>", "", []error{ErrUnavailable}, []error{ErrNotFound}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				writeXML(w, tt.status, tt.body)
			})
			_, err := c.GetEntry(context.Background(), GetEntryParams{Key: "k", PayerID: "1", EndToEndID: "e"})
			var api *APIError
			if !errors.As(err, &api) || api.Status != tt.status {
				t.Fatalf("want an *APIError with status %d, got %T %v", tt.status, err, err)
			}
			for _, want := range tt.is {
				if !errors.Is(err, want) {
					t.Errorf("errors.Is(%v) = false", want)
				}
			}
			for _, not := range tt.isNot {
				if errors.Is(err, not) {
					t.Errorf("errors.Is(%v) = true", not)
				}
			}
			if Retryable(err) != tt.retryable {
				t.Errorf("Retryable = %v, want %v", Retryable(err), tt.retryable)
			}
			if tt.retryAfter != "" && api.RetryAfter == 0 {
				t.Error("Retry-After was not read")
			}
		})
	}
}

func TestProblemDetails(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeXML(w, 400, problem("EntryInvalid", "Entry is invalid", 400,
			`<detail>bad</detail><violations><violation><reason>wrong format</reason><property>Entry.Key</property><value>x</value></violation></violations>`))
	})
	_, err := c.CreateEntry(context.Background(), Entry{KeyType: KeyCPF, Key: "x"}, "id")
	var api *APIError
	if !errors.As(err, &api) {
		t.Fatalf("got %v", err)
	}
	if api.Kind != "EntryInvalid" || api.CorrelationID != "c0ffee" || api.Detail != "bad" ||
		len(api.Violations) != 1 || api.Violations[0].Property != "Entry.Key" || api.Violations[0].Reason != "wrong format" {
		t.Fatalf("problem: %+v", api)
	}
}

func TestDictNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nobody listens any more

	c := NewDictClient(url+"/api/v2", "99000001")
	_, err := c.GetEntry(context.Background(), GetEntryParams{Key: "k", PayerID: "1", EndToEndID: "e"})
	if !errors.Is(err, ErrUnavailable) || !Retryable(err) {
		t.Fatalf("got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetEntry(ctx, GetEntryParams{Key: "k"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancelled context: %v", err)
	}
}
