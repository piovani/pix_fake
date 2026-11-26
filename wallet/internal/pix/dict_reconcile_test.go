package pix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The example of the DICT API's Reconciliation section.
func TestCIDSpecExample(t *testing.T) {
	e := Entry{
		Key: "+5511987654321", KeyType: KeyPhone,
		Owner:   Owner{TaxIDNumber: "11122233300", Name: "João Silva"},
		Account: Account{Participant: "12345678", Branch: "00001", AccountNumber: "0007654321", AccountType: AccountChecking},
	}
	got, err := CID(e, "01020304-0506-0708-090a-0b0c0d0e0f10")
	if err != nil || got != "28c06eb41c4dc9c3ae114831efcac7446c8747777fca8b145ecd31ff8480ae88" {
		t.Fatalf("cid = %s, %v", got, err)
	}
	if _, err := CID(e, "nope"); err == nil {
		t.Fatal("a request id that is not a UUID was accepted")
	}

	var v VSync
	for _, c := range []string{got, "4d4abb9168114e349672b934d16ed201a919cb49e28b7f66a240e62c92ee007f", "fce514f84f37934bc8aa0f861e4f7392273d71b9d18e8209d21e4192a7842058"} {
		if err := v.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	if v.String() != "996fc1dd3b6b14bcf0c9fe8320eb66d7e2a3fd874ccf767b2e939641b1ea8eaf" {
		t.Fatalf("vsync = %s", v)
	}
	if err := v.Add("zz"); err == nil {
		t.Fatal("not a CID was accepted")
	}
}

func TestCreateSyncVerification(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/sync-verifications" ||
			!strings.Contains(string(body), "<Participant>99000001</Participant><KeyType>CPF</KeyType><ParticipantSyncVerifier>"+ZeroVSync) {
			t.Errorf("request: %s %s %s", r.Method, r.URL.Path, body)
		}
		writeXML(w, 201, `<CreateSyncVerificationResponse><SyncVerification><Participant>99000001</Participant><KeyType>CPF</KeyType>
<ParticipantSyncVerifier>`+ZeroVSync+`</ParticipantSyncVerifier><Id>7</Id><Result>NOK</Result></SyncVerification></CreateSyncVerificationResponse>`)
	})
	res, err := c.CreateSyncVerification(context.Background(), KeyCPF, ZeroVSync)
	if err != nil || res != SyncNOK {
		t.Fatalf("%s, %v", res, err)
	}
}

func TestCIDFile(t *testing.T) {
	content := "aa\nbb\n"
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	gets := 0
	var url string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/cids/files":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "<Participant>99000001</Participant><KeyType>EMAIL</KeyType>") {
				t.Errorf("body %s", body)
			}
			writeXML(w, 201, `<CreateCidSetFileResponse><CidSetFile><Id>3</Id><Status>REQUESTED</Status><Participant>99000001</Participant><KeyType>EMAIL</KeyType><RequestTime>2026-11-25T10:00:00.000Z</RequestTime></CidSetFile></CreateCidSetFileResponse>`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/cids/files/3":
			if r.Header.Get("PI-RequestingParticipant") != "99000001" {
				t.Errorf("PI-RequestingParticipant = %q", r.Header.Get("PI-RequestingParticipant"))
			}
			gets++
			status := "REQUESTED"
			if gets > 1 {
				status = "AVAILABLE"
			}
			writeXML(w, 200, `<GetCidSetFileResponse><CidSetFile><Id>3</Id><Status>`+status+`</Status><Participant>99000001</Participant><KeyType>EMAIL</KeyType><RequestTime>2026-11-25T10:00:00.000Z</RequestTime>
<Url>`+url+`</Url><Sha256>`+sha+`</Sha256></CidSetFile></GetCidSetFileResponse>`)
		case r.URL.Path == "/files/3":
			_, _ = io.WriteString(w, content)
		case r.URL.Path == "/files/bad":
			_, _ = io.WriteString(w, "tampered\n")
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	url = strings.TrimSuffix(c.baseURL, "/api/v2") + "/files/3"

	ctx := context.Background()
	f, err := c.CreateCIDFile(ctx, KeyEmail)
	if err != nil || f.ID != 3 || f.Status != FileRequested {
		t.Fatalf("create: %+v, %v", f, err)
	}
	if _, err := c.DownloadCIDFile(ctx, f); err == nil {
		t.Fatal("a file not yet available was downloaded")
	}
	f, err = c.WaitCIDFile(ctx, f.ID, time.Millisecond)
	if err != nil || f.Status != FileAvailable || gets != 2 {
		t.Fatalf("wait: %+v, %v, %d gets", f, err, gets)
	}
	cids, err := c.DownloadCIDFile(ctx, f)
	if err != nil || len(cids) != 2 || cids[0] != "aa" || cids[1] != "bb" {
		t.Fatalf("download: %v, %v", cids, err)
	}

	f.URL = strings.TrimSuffix(c.baseURL, "/api/v2") + "/files/bad"
	if _, err := c.DownloadCIDFile(ctx, f); err == nil || !strings.Contains(err.Error(), "Sha256") {
		t.Fatalf("a file that does not match its Sha256: %v", err)
	}
}

func TestWaitCIDFileGivesUp(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeXML(w, 200, `<GetCidSetFileResponse><CidSetFile><Id>1</Id><Status>REQUESTED</Status></CidSetFile></GetCidSetFileResponse>`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.WaitCIDFile(ctx, 1, 5*time.Millisecond); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestGetEntryByCID(t *testing.T) {
	cid := strings.Repeat("ab", 32)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/cids/entries/"+cid {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		writeXML(w, 200, `<GetEntryByCidResponse><Cid>`+cid+`</Cid>
<Entry><Key>ana@example.com</Key><KeyType>EMAIL</KeyType>
<Account><Participant>99000001</Participant><Branch>0001</Branch><AccountNumber>00123456</AccountNumber><AccountType>CACC</AccountType><OpeningDate>2020-01-01T03:00:00.000Z</OpeningDate></Account>
<Owner><Type>NATURAL_PERSON</Type><TaxIdNumber>52998224725</TaxIdNumber><Name>Ana Souza</Name></Owner></Entry>
<RequestId>a946d533-7f22-42a5-9a9b-e87cd55c0f4d</RequestId></GetEntryByCidResponse>`)
	})
	e, req, err := c.GetEntryByCID(context.Background(), cid)
	if err != nil || e.Key != "ana@example.com" || e.Account.AccountNumber != "00123456" || req != "a946d533-7f22-42a5-9a9b-e87cd55c0f4d" {
		t.Fatalf("%+v, %s, %v", e, req, err)
	}
}
