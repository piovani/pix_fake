// Package contract_test checks the DICT mock against the Bacen OpenAPI file (docs/openapi.json): the examples in the
// file must be valid against its own schemas (which tests the validator), the mock must accept the requests of those
// examples, and what the mock answers, for every operation it implements and for its errors, must have a status the
// file documents for that operation and a body that follows the schema of that response.
package contract_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/fault"
	"pixfake/mock/dict/internal/httpapi"
	"pixfake/mock/dict/internal/memstore"
	"pixfake/mock/dict/internal/seed"
)

// The operations the mock implements, as the file names them.
var operations = []struct{ method, path string }{
	{"POST", "/entries"},
	{"GET", "/entries/{Key}"},
	{"PUT", "/entries/{Key}"},
	{"POST", "/entries/{Key}/delete"},
	{"POST", "/keys/check"},
	{"POST", "/sync-verifications"},
	{"POST", "/cids/files"},
	{"GET", "/cids/files/{Id}"},
	{"GET", "/cids/events"},
	{"GET", "/cids/entries/{Cid}"},
	{"POST", "/infraction-reports"},
	{"GET", "/infraction-reports"},
	{"GET", "/infraction-reports/{InfractionReportId}"},
	{"PUT", "/infraction-reports/{InfractionReportId}"},
	{"POST", "/infraction-reports/{InfractionReportId}/acknowledge"},
	{"POST", "/infraction-reports/{InfractionReportId}/cancel"},
	{"POST", "/infraction-reports/{InfractionReportId}/close"},
	{"POST", "/fraud-markers"},
	{"GET", "/fraud-markers"},
	{"GET", "/fraud-markers/{FraudMarkerId}"},
	{"POST", "/fraud-markers/{FraudMarkerId}/cancel"},
	{"POST", "/refunds"},
	{"GET", "/refunds"},
	{"GET", "/refunds/{RefundId}"},
	{"POST", "/refunds/{RefundId}/cancel"},
	{"POST", "/refunds/{RefundId}/close"},
	{"GET", "/persons/{TaxIdNumber}/statistics"},
	{"GET", "/entries/{Key}/statistics"},
	{"POST", "/funds-recoveries"},
	{"GET", "/funds-recoveries/{FundsRecoveryId}"},
	{"PUT", "/funds-recoveries/{FundsRecoveryId}"},
	{"POST", "/funds-recoveries/{FundsRecoveryId}/tracking-graph"},
	{"GET", "/funds-recoveries/{FundsRecoveryId}/tracking-graph"},
	{"POST", "/funds-recoveries/{FundsRecoveryId}/block"},
	{"GET", "/funds-recoveries/{FundsRecoveryId}/infraction-reports"},
	{"POST", "/funds-recoveries/{FundsRecoveryId}/refund"},
	{"GET", "/funds-recoveries/{FundsRecoveryId}/refunds"},
	{"POST", "/funds-recoveries/{FundsRecoveryId}/cancel"},
	{"GET", "/event-notifications"},
}

type server struct {
	t      *testing.T
	url    string
	faults *fault.Set
	svc    *dict.Service
}

func newServer(t *testing.T) *server {
	t.Helper()
	svc := dict.NewService(memstore.New())
	if _, err := svc.Seed(context.Background(), seed.Entries()); err != nil {
		t.Fatal(err)
	}
	faults := &fault.Set{}
	api := httpapi.New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), nil).WithFaults(faults)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &server{t: t, url: srv.URL, faults: faults, svc: svc}
}

type reply struct {
	status      int
	contentType string
	body        []byte
}

func (s *server) do(method, path, body string, headers map[string]string) reply {
	s.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+"/api/v2"+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return reply{resp.StatusCode, ct, raw}
}

// knownGaps are answers the mock gives that the file does not document for the operation. Each one is a sensible
// answer to a request the file leaves unspecified, and its body must still follow the error schema.
var knownGaps = map[string]string{
	"POST /keys/check 400": "the file limits Keys to 1..200 items (minItems, maxItems) but documents no 400 for it",
}

// conforms checks a reply against the file: the status is documented for the operation, the media type is the one
// of that response, and the body follows its schema.
func conforms(t *testing.T, sp *spec, method, specPath string, r reply) {
	t.Helper()
	op := sp.operation(method, specPath)
	if op == nil {
		t.Fatalf("%s %s is not in the specification", method, specPath)
	}
	schema, mediaType, documented := sp.responseSchema(op, r.status)
	if !documented {
		why, known := knownGaps[fmt.Sprintf("%s %s %d", method, specPath, r.status)]
		if !known {
			t.Errorf("%s %s answered %d, which the specification does not document\n%s", method, specPath, r.status, r.body)
			return
		}
		t.Logf("known gap: %s %s answered %d (%s)", method, specPath, r.status, why)
		schema, mediaType = map[string]any{"$ref": "#/components/schemas/Problem"}, "application/problem+xml"
	}
	if schema == nil {
		return
	}
	if mediaType != r.contentType {
		t.Errorf("%s %s %d: Content-Type is %q, the specification says %q", method, specPath, r.status, r.contentType, mediaType)
	}
	if errs := sp.validate(r.body, schema, refName(schema)); len(errs) > 0 {
		t.Errorf("%s %s %d does not follow its schema:\n  %s\n%s", method, specPath, r.status, strings.Join(errs, "\n  "), r.body)
	}
}

// --- The examples in the file ---

// Every example in the file must be valid against its own schema. This is what proves the validator reads the file
// the way its authors meant, before it is trusted to judge the mock.
func TestSpecExamples(t *testing.T) {
	sp := loadSpec(t)
	checked := 0
	for _, o := range operations {
		op := sp.operation(o.method, o.path)
		if schema, example := sp.requestBody(op); schema != nil && example != "" {
			checked++
			if errs := sp.validate([]byte(example), schema, refName(schema)); len(errs) > 0 {
				t.Errorf("request example of %s %s:\n  %s", o.method, o.path, strings.Join(errs, "\n  "))
			}
		}
		responses, _ := op["responses"].(map[string]any)
		for code := range responses {
			r := sp.resolve(responses[code])
			content, _ := r["content"].(map[string]any)
			for _, mv := range content {
				media, _ := mv.(map[string]any)
				examples, _ := media["examples"].(map[string]any)
				for name, ev := range examples {
					value, _ := ev.(map[string]any)["value"].(string)
					if value == "" {
						continue
					}
					checked++
					if errs := withoutExampleSlips(sp.validate([]byte(value), media["schema"], refName(media["schema"]))); len(errs) > 0 {
						t.Errorf("response example %q (%s) of %s %s:\n  %s", name, code, o.method, o.path, strings.Join(errs, "\n  "))
					}
				}
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d examples checked; the specification file changed shape", checked)
	}
}

// exampleSlips are mistakes of the file's examples against its own schemas. The mock follows the schemas.
var exampleSlips = []string{
	// the examples of the refunds write the RefundAccount's tax id as <TaxId>; the schema calls it TaxIdNumber
	"/RefundAccount: unexpected element <TaxId>", "/RefundAccount: missing element <TaxIdNumber>",
	// the examples of the funds recoveries, but the one of getFundsRecovery, leave out the ContactInformation the
	// schema requires
	"/FundsRecovery: missing element <ContactInformation>",
	// the example of the deprecated block gives a status the enumeration no longer has
	`/BlockFundsRecoveryResponse/FundsRecovery/Status: "BLOCKING" is not one of [CREATED TRACKED AWAITING_ANALYSIS ANALYSED REFUNDING COMPLETED CANCELLED]`,
}

func withoutExampleSlips(errs []string) []string {
	var out []string
	for _, e := range errs {
		slip := false
		for _, s := range exampleSlips {
			slip = slip || strings.HasSuffix(e, s)
		}
		if !slip {
			out = append(out, e)
		}
	}
	return out
}

// The mock must accept the requests that the file gives as examples, in the order of a key's life.
func TestSpecRequests(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)

	example := func(method, path string) string {
		_, ex := sp.requestBody(sp.operation(method, path))
		if ex == "" {
			t.Fatalf("%s %s has no request example", method, path)
		}
		return ex
	}
	create := example("POST", "/entries")
	key := between(create, "<Key>", "</Key>")
	holder := between(create, "<Participant>", "</Participant>")
	asHolder := map[string]string{httpapi.ParticipantHeader: holder}

	r := s.do("POST", "/entries", create, asHolder)
	conforms(t, sp, "POST", "/entries", r)
	if r.status != http.StatusCreated {
		t.Fatalf("createEntry with the example = %d\n%s", r.status, r.body)
	}

	// Another participant looks the key up, as a payer's institution does.
	r = s.do("GET", "/entries/"+key, "", map[string]string{
		httpapi.ParticipantHeader: "87654321", "PI-RequestingParticipant": "87654321", "PI-PayerId": "11122233300",
		"PI-EndToEndId": "E8765432120260101120000000000001",
	})
	conforms(t, sp, "GET", "/entries/{Key}", r)
	if r.status != http.StatusOK {
		t.Fatalf("getEntry = %d\n%s", r.status, r.body)
	}

	// The examples of update and delete use another key than the one of create: register that one first, the same way.
	update := example("PUT", "/entries/{Key}")
	updateKey := between(update, "<Key>", "</Key>")
	other := strings.Replace(create, key, updateKey, 1)
	other = strings.Replace(other, between(other, "<RequestId>", "</RequestId>"), "9d8c7b6a-1f2e-4d3c-8b4a-5f6e7d8c9b0a", 1)
	if r := s.do("POST", "/entries", other, asHolder); r.status != http.StatusCreated {
		t.Fatalf("registering the key of the update example = %d\n%s", r.status, r.body)
	}
	r = s.do("PUT", "/entries/"+updateKey, update, asHolder)
	conforms(t, sp, "PUT", "/entries/{Key}", r)
	if r.status != http.StatusOK {
		t.Fatalf("updateEntry with the example = %d\n%s", r.status, r.body)
	}

	r = s.do("POST", "/keys/check", example("POST", "/keys/check"), map[string]string{httpapi.ParticipantHeader: "87654321"})
	conforms(t, sp, "POST", "/keys/check", r)
	if r.status != http.StatusOK {
		t.Fatalf("checkKeys with the example = %d\n%s", r.status, r.body)
	}

	// Reconciliation, by the participant of the create example.
	r = s.do("POST", "/sync-verifications", example("POST", "/sync-verifications"), asHolder)
	conforms(t, sp, "POST", "/sync-verifications", r)
	if r.status != http.StatusCreated {
		t.Fatalf("createSyncVerification with the example = %d\n%s", r.status, r.body)
	}
	r = s.do("POST", "/cids/files", example("POST", "/cids/files"), asHolder)
	conforms(t, sp, "POST", "/cids/files", r)
	if r.status != http.StatusCreated {
		t.Fatalf("createCidSetFile with the example = %d\n%s", r.status, r.body)
	}
	fileID := between(string(r.body), "<Id>", "</Id>")
	r = s.do("GET", "/cids/files/"+fileID, "", with(asHolder, "PI-RequestingParticipant", holder))
	conforms(t, sp, "GET", "/cids/files/{Id}", r)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), "<Url>") {
		t.Fatalf("getCidSetFile = %d\n%s", r.status, r.body)
	}
	r = s.do("GET", "/cids/events?Participant="+holder+"&KeyType="+between(create, "<KeyType>", "</KeyType>"), "", asHolder)
	conforms(t, sp, "GET", "/cids/events", r)
	if r.status != http.StatusOK {
		t.Fatalf("listCidSetEvents = %d\n%s", r.status, r.body)
	}
	cid := between(string(r.body), "<Cid>", "</Cid>")
	r = s.do("GET", "/cids/entries/"+cid, "", asHolder)
	conforms(t, sp, "GET", "/cids/entries/{Cid}", r)
	if r.status != http.StatusOK {
		t.Fatalf("getEntryByCid(%q) = %d\n%s", cid, r.status, r.body)
	}

	del := example("POST", "/entries/{Key}/delete")
	r = s.do("POST", "/entries/"+between(del, "<Key>", "</Key>")+"/delete", del, asHolder)
	conforms(t, sp, "POST", "/entries/{Key}/delete", r)
	if r.status != http.StatusOK {
		t.Fatalf("deleteEntry with the example = %d\n%s", r.status, r.body)
	}
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(s[i : i+j])
}

// --- What the mock answers ---

const (
	holder    = "99000002" // Banco Alfa, which holds Carlos Mendes' keys in the seed
	requester = "99000001"
	carlos    = "39053344705"
)

var lookupHeaders = map[string]string{
	httpapi.ParticipantHeader: requester, "PI-RequestingParticipant": requester, "PI-PayerId": "52998224725",
	"PI-EndToEndId": "E9900000120261114120000000000001",
}

func with(h map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			delete(out, kv[i])
		} else {
			out[kv[i]] = kv[i+1]
		}
	}
	return out
}

func entryXML(key, keyType, participant, account, accountType, taxID, name string) string {
	return `<Entry><Key>` + key + `</Key><KeyType>` + keyType + `</KeyType><Account><Participant>` + participant + `</Participant><Branch>0001</Branch>` +
		`<AccountNumber>` + account + `</AccountNumber><AccountType>` + accountType + `</AccountType><OpeningDate>2020-03-10T03:00:00Z</OpeningDate></Account>` +
		`<Owner><Type>NATURAL_PERSON</Type><TaxIdNumber>` + taxID + `</TaxIdNumber><Name>` + name + `</Name></Owner></Entry>`
}

func createBody(entry, reason, requestID string) string {
	return `<CreateEntryRequest>` + entry + `<Reason>` + reason + `</Reason><RequestId>` + requestID + `</RequestId></CreateEntryRequest>`
}

// carlosCID is the CID of Carlos Mendes' CPF key in the seed, held by Banco Alfa.
func carlosCID(t *testing.T) string {
	t.Helper()
	for _, e := range seed.Entries() {
		if e.Key == carlos {
			cid, err := dict.CID(e, dict.SeedRequestID(e.Key))
			if err != nil {
				t.Fatal(err)
			}
			return cid
		}
	}
	t.Fatal("Carlos Mendes is not in the seed")
	return ""
}

func syncBody(participant, keyType, verifier string) string {
	return `<CreateSyncVerificationRequest><SyncVerification><Participant>` + participant + `</Participant><KeyType>` + keyType +
		`</KeyType><ParticipantSyncVerifier>` + verifier + `</ParticipantSyncVerifier></SyncVerification></CreateSyncVerificationRequest>`
}

func cidFileBody(participant, keyType string) string {
	return `<CreateCidSetFileRequest><Participant>` + participant + `</Participant><KeyType>` + keyType + `</KeyType></CreateCidSetFileRequest>`
}

func TestSpecAnswers(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)
	cid := carlosCID(t)
	asRequester := map[string]string{httpapi.ParticipantHeader: requester}
	asHolder := map[string]string{httpapi.ParticipantHeader: holder}
	newEntry := entryXML("+5521988887777", "PHONE", holder, "00700001", "CACC", carlos, "Carlos Mendes")
	const req1, req2 = "5c1f4ac6-8d2e-4f6b-9a4e-1f2d3c4b5a69", "6d2f5bd7-9e3f-4a7c-8b5f-2a3e4d5c6b7a"

	type step struct {
		name         string
		method, spec string // the operation, as the file names it
		path, body   string
		headers      map[string]string
		want         int
	}
	steps := []step{
		{"create", "POST", "/entries", "/entries", createBody(newEntry, "USER_REQUESTED", req1), asHolder, 201},
		{"create again with the same RequestId", "POST", "/entries", "/entries", createBody(newEntry, "USER_REQUESTED", req1), asHolder, 201},
		{"create a key that exists", "POST", "/entries", "/entries", createBody(newEntry, "USER_REQUESTED", req2), asHolder, 409},
		{"create with invalid fields", "POST", "/entries", "/entries", createBody(strings.Replace(newEntry, "+5521988887777", "21988887777", 1), "USER_REQUESTED", req2), asHolder, 400},
		{"create for another participant", "POST", "/entries", "/entries", createBody(newEntry, "USER_REQUESTED", req2), map[string]string{httpapi.ParticipantHeader: "12345678"}, 403},
		{"create with a RequestId used for something else", "POST", "/entries", "/entries",
			createBody(entryXML("+5521988886666", "PHONE", holder, "00700001", "CACC", carlos, "Carlos Mendes"), "USER_REQUESTED", req1), asHolder, 400},
		{"create on a salary account", "POST", "/entries", "/entries", createBody(entryXML("+5521988885555", "PHONE", holder, "00700002", "SLRY", carlos, "Carlos Mendes"), "USER_REQUESTED", req2), asHolder, 400},

		{"get", "GET", "/entries/{Key}", "/entries/" + carlos, "", lookupHeaders, 200},
		{"get with statistics", "GET", "/entries/{Key}", "/entries/" + carlos + "?IncludeStatistics=true", "", lookupHeaders, 200},
		{"get an unknown key", "GET", "/entries/{Key}", "/entries/mail@mail.com", "", lookupHeaders, 404},
		{"get without the PI headers", "GET", "/entries/{Key}", "/entries/" + carlos, "", with(lookupHeaders, "PI-PayerId", "", "PI-EndToEndId", ""), 400},
		{"get with a malformed key", "GET", "/entries/{Key}", "/entries/not-a-key", "", lookupHeaders, 400},
		{"get a key of the requester itself", "GET", "/entries/{Key}", "/entries/52998224725", "", lookupHeaders, 400},

		{"update", "PUT", "/entries/{Key}", "/entries/+5521988887777",
			`<UpdateEntryRequest><Key>+5521988887777</Key><Owner><Type>NATURAL_PERSON</Type><TaxIdNumber>` + carlos + `</TaxIdNumber><Name>Carlos Mendes Junior</Name></Owner><Reason>USER_REQUESTED</Reason></UpdateEntryRequest>`, asHolder, 200},
		{"update an unknown key", "PUT", "/entries/{Key}", "/entries/+5521900000000",
			`<UpdateEntryRequest><Key>+5521900000000</Key><Reason>USER_REQUESTED</Reason></UpdateEntryRequest>`, asHolder, 404},
		{"update a key of another participant", "PUT", "/entries/{Key}", "/entries/+5521988887777",
			`<UpdateEntryRequest><Key>+5521988887777</Key><Reason>USER_REQUESTED</Reason></UpdateEntryRequest>`, map[string]string{httpapi.ParticipantHeader: "12345678"}, 403},
		{"update with an invalid reason", "PUT", "/entries/{Key}", "/entries/+5521988887777",
			`<UpdateEntryRequest><Key>+5521988887777</Key><Reason>FRAUD</Reason></UpdateEntryRequest>`, asHolder, 400},

		{"check keys", "POST", "/keys/check", "/keys/check", `<CheckKeysRequest><Keys><Key>` + carlos + `</Key><Key>mail@mail.com</Key></Keys></CheckKeysRequest>`, asHolder, 200},
		{"check no keys", "POST", "/keys/check", "/keys/check", `<CheckKeysRequest><Keys></Keys></CheckKeysRequest>`, asHolder, 400},

		{"sync verification that agrees", "POST", "/sync-verifications", "/sync-verifications", syncBody(holder, "CPF", cid), asHolder, 201},
		{"sync verification that disagrees", "POST", "/sync-verifications", "/sync-verifications", syncBody(holder, "CPF", dict.ZeroVSync), asHolder, 201},
		{"sync verification of another participant", "POST", "/sync-verifications", "/sync-verifications", syncBody(holder, "CPF", cid), asRequester, 403},
		{"sync verification with a bad verifier", "POST", "/sync-verifications", "/sync-verifications", syncBody(holder, "CPF", "abc"), asHolder, 400},
		{"sync verification of an unknown key type", "POST", "/sync-verifications", "/sync-verifications", syncBody(holder, "IBAN", cid), asHolder, 400},

		{"CID file", "POST", "/cids/files", "/cids/files", cidFileBody(holder, "CPF"), asHolder, 201},
		{"CID file of another participant", "POST", "/cids/files", "/cids/files", cidFileBody(holder, "CPF"), asRequester, 403},
		{"CID file of an unknown key type", "POST", "/cids/files", "/cids/files", cidFileBody(holder, "IBAN"), asHolder, 400},
		{"get the CID file", "GET", "/cids/files/{Id}", "/cids/files/1", "", with(asHolder, "PI-RequestingParticipant", holder), 200},
		{"get a CID file of another participant", "GET", "/cids/files/{Id}", "/cids/files/1", "", with(asRequester, "PI-RequestingParticipant", requester), 403},
		{"get a CID file without PI-RequestingParticipant", "GET", "/cids/files/{Id}", "/cids/files/1", "", asHolder, 403},
		{"get an unknown CID file", "GET", "/cids/files/{Id}", "/cids/files/999", "", with(asHolder, "PI-RequestingParticipant", holder), 404},

		{"CID events", "GET", "/cids/events", "/cids/events?Participant=" + holder + "&KeyType=CPF&Limit=10", "", asHolder, 200},
		{"CID events in a window", "GET", "/cids/events", "/cids/events?Participant=" + holder + "&KeyType=CPF&StartTime=2020-01-01T00:00:00Z&EndTime=2099-01-01T00:00:00Z", "", asHolder, 200},
		{"CID events of another participant", "GET", "/cids/events", "/cids/events?Participant=" + holder + "&KeyType=CPF", "", asRequester, 403},
		{"CID events with a bad limit", "GET", "/cids/events", "/cids/events?Participant=" + holder + "&KeyType=CPF&Limit=500", "", asHolder, 400},
		{"CID events without a key type", "GET", "/cids/events", "/cids/events?Participant=" + holder, "", asHolder, 400},

		{"entry by CID", "GET", "/cids/entries/{Cid}", "/cids/entries/" + cid, "", asHolder, 200},
		{"entry by CID of another participant", "GET", "/cids/entries/{Cid}", "/cids/entries/" + cid, "", asRequester, 403},
		{"entry by an unknown CID", "GET", "/cids/entries/{Cid}", "/cids/entries/" + dict.ZeroVSync, "", asHolder, 404},

		{"delete a key of another participant", "POST", "/entries/{Key}/delete", "/entries/+5521988887777/delete",
			`<DeleteEntryRequest><Key>+5521988887777</Key><Participant>` + holder + `</Participant><Reason>USER_REQUESTED</Reason></DeleteEntryRequest>`, map[string]string{httpapi.ParticipantHeader: "12345678"}, 403},
		{"delete", "POST", "/entries/{Key}/delete", "/entries/+5521988887777/delete",
			`<DeleteEntryRequest><Key>+5521988887777</Key><Participant>` + holder + `</Participant><Reason>USER_REQUESTED</Reason></DeleteEntryRequest>`, asHolder, 200},
		{"delete again", "POST", "/entries/{Key}/delete", "/entries/+5521988887777/delete",
			`<DeleteEntryRequest><Key>+5521988887777</Key><Participant>` + holder + `</Participant><Reason>USER_REQUESTED</Reason></DeleteEntryRequest>`, asHolder, 404},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			r := s.do(st.method, st.path, st.body, st.headers)
			if r.status != st.want {
				t.Fatalf("status = %d, want %d\n%s", r.status, st.want, r.body)
			}
			conforms(t, sp, st.method, st.spec, r)
		})
	}
}

// The failures a test can ask for (429 and 503) are answered the way the file documents them.
func TestSpecFaults(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)

	if _, err := s.faults.Add(fault.Rule{Status: 429, RetryAfter: 1, Count: 1}); err != nil {
		t.Fatal(err)
	}
	r := s.do("GET", "/entries/"+carlos, "", lookupHeaders)
	if r.status != 429 {
		t.Fatalf("status = %d", r.status)
	}
	conforms(t, sp, "GET", "/entries/{Key}", r)

	for _, o := range operations {
		if o.method == "GET" {
			continue // getEntry documents 429 but not 503
		}
		if _, err := s.faults.Add(fault.Rule{Status: 503, Count: 1}); err != nil {
			t.Fatal(err)
		}
		path := strings.NewReplacer("{Key}", "+5521988887777", "{Id}", "1", "{Cid}", dict.ZeroVSync,
			"{InfractionReportId}", unknownID, "{FraudMarkerId}", unknownID, "{RefundId}", unknownID,
			"{FundsRecoveryId}", unknownID).Replace(o.path)
		r := s.do(o.method, path, "<x/>", map[string]string{httpapi.ParticipantHeader: holder})
		if r.status != 503 {
			t.Fatalf("%s %s = %d", o.method, o.path, r.status)
		}
		conforms(t, sp, o.method, o.path, r)
	}
}

// Every operation the mock implements is in the file, with the same method and path.
func TestSpecOperations(t *testing.T) {
	sp := loadSpec(t)
	for _, o := range operations {
		if sp.operation(o.method, o.path) == nil {
			t.Errorf("%s %s is not in the specification", o.method, o.path)
		}
	}
}
