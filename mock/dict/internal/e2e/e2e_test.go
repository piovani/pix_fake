// Package e2e testa a API HTTP completa sobre um Postgres real.
package e2e_test

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/httpapi"
	"pixfake/mock/dict/internal/postgres"
	"pixfake/mock/dict/internal/postgres/pgtest"
	"pixfake/mock/dict/internal/seed"
)

const (
	base = httpapi.BasePath
	psp  = "12345678"
)

type client struct {
	t   *testing.T
	url string
	svc *dict.Service
}

type reply struct {
	status      int
	contentType string
	body        string
}

// newClient sobe a API real (handlers + service + Postgres) em um httptest.Server.
// Sem DICT_TEST_DATABASE_URL o teste é pulado (ver pgtest).
func newClient(t *testing.T) *client {
	t.Helper()
	pool := pgtest.NewPool(t)

	svc := dict.NewService(postgres.NewRepository(pool))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpapi.New(svc, log, pool.Ping).Handler())
	t.Cleanup(srv.Close)
	return &client{t: t, url: srv.URL, svc: svc}
}

func (c *client) do(method, path, body string, headers map[string]string) reply {
	c.t.Helper()
	req, err := http.NewRequest(method, c.url+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set(httpapi.ParticipantHeader, psp)
	req.Header.Set("Content-Type", "application/xml")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return reply{res.StatusCode, res.Header.Get("Content-Type"), string(out)}
}

func (r reply) want(t *testing.T, status int, parts ...string) reply {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d\n%s", r.status, status, r.body)
	}
	for _, p := range parts {
		if !strings.Contains(r.body, p) {
			t.Fatalf("body does not contain %q:\n%s", p, r.body)
		}
	}
	return r
}

var getHeaders = map[string]string{
	"PI-RequestingParticipant": psp,
	"PI-PayerId":               "11122233300",
	"PI-EndToEndId":            "E1234567820240101120000000000001",
}

func createBody(keyElems, requestID string) string {
	return `<CreateEntryRequest>
    <Entry>` + keyElems + `
        <Account>
            <Participant>` + psp + `</Participant>
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
    <RequestId>` + requestID + `</RequestId>
</CreateEntryRequest>`
}

const (
	phoneKey = "+5561988880000"
	phone    = `<Key>` + phoneKey + `</Key><KeyType>PHONE</KeyType>`
	reqA     = "a946d533-7f22-42a5-9a9b-e87cd55c0f4d"
	reqB     = "b946d533-7f22-42a5-9a9b-e87cd55c0f4d"
)

func TestEntryLifecycleOnPostgres(t *testing.T) {
	c := newClient(t)

	// Criação e repetição idempotente devolvem o mesmo vínculo (mesmas datas, vindas do banco).
	first := c.do("POST", base+"/entries", createBody(phone, reqA), nil).
		want(t, http.StatusCreated, "<Key>"+phoneKey+"</Key>", "<CreationDate>")
	again := c.do("POST", base+"/entries", createBody(phone, reqA), nil).want(t, http.StatusCreated)
	if creation(t, first.body) != creation(t, again.body) {
		t.Fatalf("replay returned a different CreationDate:\n%s\n%s", first.body, again.body)
	}

	// Mesmo RequestId com outros dados e mesma chave com outro RequestId são recusados.
	other := strings.Replace(createBody(phone, reqA), "0007654321", "0000000001", 1)
	c.do("POST", base+"/entries", other, nil).want(t, http.StatusBadRequest, "/error/RequestIdAlreadyUsed")
	c.do("POST", base+"/entries", createBody(phone, reqB), nil).want(t, http.StatusConflict, "/error/ResourceConflict")

	// Consulta (com e sem estatísticas), por outro participante identificado.
	c.do("GET", base+"/entries/"+phoneKey, "", getHeaders).
		want(t, http.StatusOK, "<Name>João Silva</Name>", "<OpeningDate>2010-01-10T03:00:00.000Z</OpeningDate>")
	c.do("GET", base+"/entries/"+phoneKey+"?IncludeStatistics=true", "", getHeaders).
		want(t, http.StatusOK, "<OwnerStatistics>", "<KeyStatistics>")

	// Atualização persiste conta e nome.
	update := `<UpdateEntryRequest><Key>` + phoneKey + `</Key>
        <Account><Participant>` + psp + `</Participant><Branch>0002</Branch><AccountNumber>0009999999</AccountNumber>
        <AccountType>SVGS</AccountType><OpeningDate>2010-01-10T03:00:00Z</OpeningDate></Account>
        <Owner><Type>NATURAL_PERSON</Type><TaxIdNumber>11122233300</TaxIdNumber><Name>João Souza</Name></Owner>
        <Reason>USER_REQUESTED</Reason></UpdateEntryRequest>`
	c.do("PUT", base+"/entries/"+phoneKey, update, nil).
		want(t, http.StatusOK, "<AccountNumber>0009999999</AccountNumber>", "<AccountType>SVGS</AccountType>")
	c.do("GET", base+"/entries/"+phoneKey, "", getHeaders).
		want(t, http.StatusOK, "<Name>João Souza</Name>", "<Branch>0002</Branch>")

	// Outro participante não altera nem remove.
	otherPSP := map[string]string{httpapi.ParticipantHeader: "87654321"}
	c.do("PUT", base+"/entries/"+phoneKey, update, otherPSP).want(t, http.StatusForbidden)
	del := `<DeleteEntryRequest><Key>` + phoneKey + `</Key><Participant>` + psp + `</Participant><Reason>ACCOUNT_CLOSURE</Reason></DeleteEntryRequest>`
	c.do("POST", base+"/entries/"+phoneKey+"/delete", del, otherPSP).want(t, http.StatusForbidden)

	// Verificação de chaves reflete o banco.
	check := `<CheckKeysRequest><Keys><Key>` + phoneKey + `</Key><Key>mail@mail.com</Key></Keys></CheckKeysRequest>`
	c.do("POST", base+"/keys/check", check, nil).
		want(t, http.StatusOK, `<Key hasEntry="true">`+phoneKey+`</Key>`, `<Key hasEntry="false">mail@mail.com</Key>`)

	// Remoção: depois dela a chave some e não pode ser removida de novo.
	c.do("POST", base+"/entries/"+phoneKey+"/delete", del, nil).
		want(t, http.StatusOK, "<DeleteEntryResponse>", "<Key>"+phoneKey+"</Key>")
	c.do("GET", base+"/entries/"+phoneKey, "", getHeaders).want(t, http.StatusNotFound)
	c.do("POST", base+"/entries/"+phoneKey+"/delete", del, nil).want(t, http.StatusNotFound)
	c.do("POST", base+"/keys/check", check, nil).
		want(t, http.StatusOK, `<Key hasEntry="false">`+phoneKey+`</Key>`)
}

func TestEVPKeyIsGeneratedAndPersisted(t *testing.T) {
	c := newClient(t)

	res := c.do("POST", base+"/entries", createBody(`<KeyType>EVP</KeyType>`, reqA), nil).want(t, http.StatusCreated)
	var created struct{ Entry struct{ Key string } }
	if err := xml.Unmarshal([]byte(res.body), &created); err != nil {
		t.Fatal(err)
	}
	if kt, ok := dict.DetectKeyType(created.Entry.Key); !ok || kt != dict.KeyEVP {
		t.Fatalf("generated key %q is not EVP", created.Entry.Key)
	}

	c.do("GET", base+"/entries/"+created.Entry.Key, "", getHeaders).
		want(t, http.StatusOK, "<Key>"+created.Entry.Key+"</Key>", "<KeyType>EVP</KeyType>")

	// Repetir o RequestId devolve a mesma chave EVP, em vez de gerar outra.
	c.do("POST", base+"/entries", createBody(`<KeyType>EVP</KeyType>`, reqA), nil).
		want(t, http.StatusCreated, "<Key>"+created.Entry.Key+"</Key>")
}

func TestProblemResponsesOnPostgres(t *testing.T) {
	c := newClient(t)

	c.do("GET", "/healthz", "", map[string]string{httpapi.ParticipantHeader: ""}).want(t, http.StatusOK, "ok")

	res := c.do("GET", base+"/entries/mail@mail.com", "", getHeaders).
		want(t, http.StatusNotFound, `<problem xmlns="urn:ietf:rfc:7807">`, "/error/NotFound")
	if res.contentType != "application/problem+xml" {
		t.Fatalf("content-type = %q", res.contentType)
	}
	c.do("POST", base+"/keys/check", `<CheckKeysRequest><Keys><Key>a@b.co</Key></Keys></CheckKeysRequest>`,
		map[string]string{httpapi.ParticipantHeader: ""}).want(t, http.StatusForbidden)
	c.do("POST", base+"/entries", createBody(`<Key>61988887777</Key><KeyType>PHONE</KeyType>`, reqA), nil).
		want(t, http.StatusBadRequest, "/error/EntryInvalid", "<property>entry.key</property>")
}

func creation(t *testing.T, body string) string {
	t.Helper()
	var out struct{ Entry struct{ CreationDate string } }
	if err := xml.Unmarshal([]byte(body), &out); err != nil || out.Entry.CreationDate == "" {
		t.Fatalf("no CreationDate in response (%v):\n%s", err, body)
	}
	return out.Entry.CreationDate
}

func TestSeededKeysAreServedByTheAPI(t *testing.T) {
	c := newClient(t)
	entries := seed.Entries()

	res, err := c.svc.Seed(context.Background(), entries)
	if err != nil || len(res.Created) != len(entries) {
		t.Fatalf("seed = %+v, %v", res, err)
	}

	// Cada chave do seed pode ser consultada, como a carteira faria antes de pagar.
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
		c.do("GET", base+"/entries/"+e.Key, "", getHeaders).want(t, http.StatusOK,
			"<Key>"+e.Key+"</Key>", "<Participant>"+e.Account.Participant+"</Participant>", "<Name>"+e.Owner.Name+"</Name>")
	}

	// A verificação em lote enxerga todas.
	body := "<CheckKeysRequest><Keys>"
	for _, k := range keys {
		body += "<Key>" + k + "</Key>"
	}
	body += "</Keys></CheckKeysRequest>"
	res2 := c.do("POST", base+"/keys/check", body, nil).want(t, http.StatusOK)
	if n := strings.Count(res2.body, `hasEntry="true"`); n != len(keys) {
		t.Fatalf("%d of %d seeded keys reported as existing", n, len(keys))
	}

	// Já cadastradas, não podem ser criadas de novo pela API.
	c.do("POST", base+"/entries", createBody(`<Key>`+keys[1]+`</Key><KeyType>PHONE</KeyType>`, reqB), nil).
		want(t, http.StatusConflict, "/error/ResourceConflict")

	// Rodar o seed outra vez não muda nada.
	res, err = c.svc.Seed(context.Background(), entries)
	if err != nil || len(res.Created) != 0 || len(res.Existing) != len(entries) {
		t.Fatalf("second seed = %+v, %v", res, err)
	}
}
