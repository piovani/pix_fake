package httpapi_test

import (
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/httpapi"
	"pixfake/mock/dict/internal/memstore"
)

// Bodies copied from the DICT specification examples (v2.13.0).
const (
	createReq = `<?xml version="1.0" encoding="UTF-8" ?>
<CreateEntryRequest>
    <Signature></Signature>
    <Entry>
        <Key>+5561988880000</Key>
        <KeyType>PHONE</KeyType>
        <Account>
            <Participant>12345678</Participant>
            <Branch>0001</Branch>
            <AccountNumber>0007654321</AccountNumber>
            <AccountType>CACC</AccountType>
            <OpeningDate>2010-01-10T03:00:00Z</OpeningDate>
        </Account>
        <Owner>
            <Type>NATURAL_PERSON</Type>
            <TaxIdNumber>11122233300</TaxIdNumber>
            <Name>João Silva</Name>
        </Owner>
    </Entry>
    <Reason>USER_REQUESTED</Reason>
    <RequestId>a946d533-7f22-42a5-9a9b-e87cd55c0f4d</RequestId>
</CreateEntryRequest>`

	updateReq = `<?xml version="1.0" encoding="UTF-8" ?>
<UpdateEntryRequest>
    <Signature></Signature>
    <Key>+5561988880000</Key>
    <Account>
        <Participant>12345678</Participant>
        <Branch>0001</Branch>
        <AccountNumber>0009999999</AccountNumber>
        <AccountType>CACC</AccountType>
        <OpeningDate>2010-01-10T03:00:00Z</OpeningDate>
    </Account>
    <Owner>
        <Type>NATURAL_PERSON</Type>
        <TaxIdNumber>11122233300</TaxIdNumber>
        <Name>João Souza</Name>
    </Owner>
    <Reason>USER_REQUESTED</Reason>
</UpdateEntryRequest>`

	deleteReq = `<?xml version="1.0" encoding="UTF-8" ?>
<DeleteEntryRequest>
    <Signature></Signature>
    <Key>+5561988880000</Key>
    <Participant>12345678</Participant>
    <Reason>ACCOUNT_CLOSURE</Reason>
</DeleteEntryRequest>`

	checkReq = `<?xml version="1.0" encoding="UTF-8" ?>
<CheckKeysRequest>
    <Keys>
        <Key>+5561988880000</Key>
        <Key>mail2@mail.com</Key>
    </Keys>
</CheckKeysRequest>`
)

const base = httpapi.BasePath

func newHandler() http.Handler {
	svc := dict.NewService(memstore.New())
	return httpapi.New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), nil).Handler()
}

type response struct {
	status      int
	contentType string
	body        string
}

func do(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(httpapi.ParticipantHeader, "12345678")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	return response{rec.Code, rec.Header().Get("Content-Type"), string(out)}
}

func (r response) wantStatus(t *testing.T, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d\n%s", r.status, status, r.body)
	}
}

func (r response) wantContains(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(r.body, p) {
			t.Fatalf("body does not contain %q:\n%s", p, r.body)
		}
	}
}

var getHeaders = map[string]string{
	"PI-RequestingParticipant": "12345678",
	"PI-PayerId":               "11122233300",
	"PI-EndToEndId":            "E1234567820240101120000000000001",
}

func TestEntryLifecycle(t *testing.T) {
	h := newHandler()

	// Create
	res := do(t, h, "POST", base+"/entries", createReq, nil)
	res.wantStatus(t, http.StatusCreated)
	res.wantContains(t, "<CreateEntryResponse>", "<Signature></Signature>", "<Key>+5561988880000</Key>",
		"<CreationDate>", "<KeyOwnershipDate>", "<CorrelationId>")
	if res.contentType != "application/xml" {
		t.Fatalf("content-type = %q", res.contentType)
	}

	// Repeating the same request is idempotent.
	do(t, h, "POST", base+"/entries", createReq, nil).wantStatus(t, http.StatusCreated)

	// Get
	res = do(t, h, "GET", base+"/entries/+5561988880000", "", getHeaders)
	res.wantStatus(t, http.StatusOK)
	res.wantContains(t, "<GetEntryResponse>", "<Name>João Silva</Name>")
	if strings.Contains(res.body, "OwnerStatistics") {
		t.Fatal("statistics must be omitted unless IncludeStatistics=true")
	}

	res = do(t, h, "GET", base+"/entries/+5561988880000?IncludeStatistics=true", "", getHeaders)
	res.wantStatus(t, http.StatusOK)
	res.wantContains(t, "<OwnerStatistics>", "<KeyStatistics>", `<Settlements d90="0" m12="0" m60="0"></Settlements>`)

	// Update
	res = do(t, h, "PUT", base+"/entries/+5561988880000", updateReq, nil)
	res.wantStatus(t, http.StatusOK)
	res.wantContains(t, "<UpdateEntryResponse>", "<AccountNumber>0009999999</AccountNumber>", "<Name>João Souza</Name>")

	// Check keys
	res = do(t, h, "POST", base+"/keys/check", checkReq, nil)
	res.wantStatus(t, http.StatusOK)
	res.wantContains(t, `<Key hasEntry="true">+5561988880000</Key>`, `<Key hasEntry="false">mail2@mail.com</Key>`)

	// Delete
	res = do(t, h, "POST", base+"/entries/+5561988880000/delete", deleteReq, nil)
	res.wantStatus(t, http.StatusOK)
	res.wantContains(t, "<DeleteEntryResponse>", "<Key>+5561988880000</Key>")

	do(t, h, "GET", base+"/entries/+5561988880000", "", getHeaders).wantStatus(t, http.StatusNotFound)
}

func TestProblems(t *testing.T) {
	h := newHandler()
	do(t, h, "POST", base+"/entries", createReq, nil).wantStatus(t, http.StatusCreated)

	t.Run("missing identification header", func(t *testing.T) {
		res := do(t, h, "POST", base+"/keys/check", checkReq, map[string]string{httpapi.ParticipantHeader: ""})
		res.wantStatus(t, http.StatusForbidden)
		res.wantContains(t, `<problem xmlns="urn:ietf:rfc:7807">`,
			"<type>https://dict.pi.rsfn.net.br/api/v2/error/Forbidden</type>", "<status>403</status>")
		if res.contentType != "application/problem+xml" {
			t.Fatalf("content-type = %q", res.contentType)
		}
	})

	t.Run("another participant cannot delete", func(t *testing.T) {
		res := do(t, h, "POST", base+"/entries/+5561988880000/delete", deleteReq,
			map[string]string{httpapi.ParticipantHeader: "87654321"})
		res.wantStatus(t, http.StatusForbidden)
	})

	t.Run("unknown key", func(t *testing.T) {
		res := do(t, h, "GET", base+"/entries/mail@mail.com", "", getHeaders)
		res.wantStatus(t, http.StatusNotFound)
		res.wantContains(t, "<detail>Entry associated with given key does not exist</detail>")
	})

	t.Run("invalid entry returns violations", func(t *testing.T) {
		bad := strings.Replace(createReq, "<Key>+5561988880000</Key>", "<Key>61988887777</Key>", 1)
		res := do(t, h, "POST", base+"/entries", bad, nil)
		res.wantStatus(t, http.StatusBadRequest)
		res.wantContains(t, "/error/EntryInvalid", "<violations>", "<property>entry.key</property>")
	})

	t.Run("malformed XML", func(t *testing.T) {
		res := do(t, h, "POST", base+"/entries", "<CreateEntryRequest>", nil)
		res.wantStatus(t, http.StatusBadRequest)
		res.wantContains(t, "<detail>Could not parse request body</detail>")
	})

	t.Run("wrong root element", func(t *testing.T) {
		do(t, h, "POST", base+"/keys/check", deleteReq, nil).wantStatus(t, http.StatusBadRequest)
	})

	t.Run("lookup without PI-* headers", func(t *testing.T) {
		res := do(t, h, "GET", base+"/entries/+5561988880000", "", nil)
		res.wantStatus(t, http.StatusBadRequest)
		res.wantContains(t, "PI-RequestingParticipant", "PI-PayerId", "PI-EndToEndId")
	})

	t.Run("unknown route", func(t *testing.T) {
		do(t, h, "GET", base+"/nope", "", nil).wantStatus(t, http.StatusNotFound)
	})
}

func TestCreateEVPReturnsGeneratedKey(t *testing.T) {
	body := strings.NewReplacer(
		"<Key>+5561988880000</Key>\n        <KeyType>PHONE</KeyType>", "<KeyType>EVP</KeyType>",
	).Replace(createReq)
	res := do(t, newHandler(), "POST", base+"/entries", body, nil)
	res.wantStatus(t, http.StatusCreated)

	var out struct {
		Entry struct{ Key string }
	}
	if err := xml.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatal(err)
	}
	if kt, ok := dict.DetectKeyType(out.Entry.Key); !ok || kt != dict.KeyEVP {
		t.Fatalf("generated key %q is not EVP\n%s", out.Entry.Key, res.body)
	}
}

func TestHealthz(t *testing.T) {
	res := do(t, newHandler(), "GET", "/healthz", "", map[string]string{httpapi.ParticipantHeader: ""})
	res.wantStatus(t, http.StatusOK)
}
