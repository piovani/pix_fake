// Package e2e is the closing test of the two mocks together: it plays a participant (the wallet and, for the
// other side of a payment, a bank) against a running DICT mock and SPI mock, over HTTP and XML only.
//
// It knows nothing about the mocks' code. It builds the messages by hand from the Bacen contracts and reads
// the answers the way a real participant would, so it also shows how to talk to them.
//
// Run it with `make e2e` at the repository root, which starts everything. By hand, start both mocks, seed them,
// and set DICT_URL and SPI_URL; without them the tests are skipped.
package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The fictional institutions and people of the shared seed.
const (
	wallet = "99000001"
	alfa   = "99000002"

	anaCPF    = "52998224725" // wallet customer, account 0001/00123456
	brunoCPF  = "11144477735" // wallet customer, account 0001/00234567
	carlosCPF = "39053344705" // Banco Alfa customer, account 0001/00456789

	eduardoCPF  = "24681357928" // Banco Alfa, closed account
	fernandaCPF = "13579246828" // Banco Alfa, blocked account
	gustavoCPF  = "86420975310" // Banco Beta, account that never answers
)

type env struct {
	t        *testing.T
	dictURL  string
	spiURL   string
	deadline time.Duration
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dict, spi := os.Getenv("DICT_URL"), os.Getenv("SPI_URL")
	if dict == "" || spi == "" {
		t.Skip("DICT_URL and SPI_URL are not set; run `make e2e` at the repository root")
	}
	http.DefaultClient.Timeout = 30 * time.Second // a stuck read must not hang the whole run
	for _, u := range []string{dict, spi} {
		resp, err := http.Get(u + "/healthz")
		if err != nil {
			t.Fatalf("%s is not reachable: %v", u, err)
		}
		resp.Body.Close()
	}
	return &env{t: t, dictURL: dict, spiURL: spi, deadline: 15 * time.Second}
}

// --- Identifiers ---

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alnum[int(b[i])%len(alnum)]
	}
	return string(b)
}

// id builds an EndToEndId ("E") or RtrId ("D"): letter + ISPB + yyyyMMddHHmm (UTC) + 11 characters.
func id(letter, ispb string) string {
	return letter + ispb + time.Now().UTC().Format("200601021504") + random(11)
}

// msgID builds a message id: M + the sender's ISPB + 23 characters.
func msgID(ispb string) string { return "M" + ispb + random(23) }

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// --- DICT ---

type entry struct {
	Key     string `xml:"Key"`
	KeyType string `xml:"KeyType"`
	Account struct {
		Participant   string `xml:"Participant"`
		Branch        string `xml:"Branch"`
		AccountNumber string `xml:"AccountNumber"`
		AccountType   string `xml:"AccountType"`
	} `xml:"Account"`
	Owner struct {
		TaxIDNumber string `xml:"TaxIdNumber"`
		Name        string `xml:"Name"`
	} `xml:"Owner"`
}

// lookup asks the DICT who holds a key, the way a payer's participant does before a payment. It returns the
// HTTP status and, for 200, the entry; for an error, the last part of the problem type.
func (e *env) lookup(requester, payerTaxID, endToEnd, key string) (status int, ent entry, problem string) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.dictURL+"/api/v2/entries/"+key, nil)
	req.Header.Set("X-Participant-ISPB", requester)
	req.Header.Set("PI-RequestingParticipant", requester)
	req.Header.Set("PI-PayerId", payerTaxID)
	req.Header.Set("PI-EndToEndId", endToEnd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var p struct {
			Type string `xml:"type"`
		}
		_ = xml.Unmarshal(body, &p)
		return resp.StatusCode, entry{}, p.Type[strings.LastIndex(p.Type, "/")+1:]
	}
	var r struct {
		Entry entry `xml:"Entry"`
	}
	if err := xml.Unmarshal(body, &r); err != nil {
		e.t.Fatalf("GetEntryResponse: %v\n%s", err, body)
	}
	return resp.StatusCode, r.Entry, ""
}

// --- SPI: sending ---

func (e *env) send(ispb string, body string) {
	e.t.Helper()
	resp, err := http.Post(e.spiURL+"/api/v1/in/"+ispb+"/msgs", "application/xml; charset=utf-8", strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("send as %s = %d: %s", ispb, resp.StatusCode, out)
	}
}

func header(from, to, msg, defID string) string {
	party := func(ispb string) string {
		return `<FIId><FinInstnId><Othr><Id>` + ispb + `</Id></Othr></FinInstnId></FIId>`
	}
	return `<AppHdr><Fr>` + party(from) + `</Fr><To>` + party(to) + `</To><BizMsgIdr>` + msg + `</BizMsgIdr>` +
		`<MsgDefIdr>` + defID + `</MsgDefIdr><CreDt>` + stamp(time.Now()) + `</CreDt><Sgntr/></AppHdr>`
}

func agent(tag, ispb string) string {
	return `<` + tag + `><FinInstnId><ClrSysMmbId><MmbId>` + ispb + `</MmbId></ClrSysMmbId></FinInstnId></` + tag + `>`
}

// payer is the paying customer of a pacs.008.
type payer struct{ ispb, branch, account, accountType, taxID, name string }

// pacs008 builds a payment order from payer to the account the DICT returned for the key.
func pacs008(from payer, to entry, key, e2e, amount string) string {
	m := msgID(from.ispb)
	return `<?xml version="1.0" encoding="UTF-8"?><Envelope xmlns="https://www.bcb.gov.br/pi/pacs.008/1.16">` +
		header(from.ispb, "00038166", m, "pacs.008.spi.1.16") +
		`<Document><FIToFICstmrCdtTrf><GrpHdr><MsgId>` + m + `</MsgId><CreDtTm>` + stamp(time.Now()) + `</CreDtTm><NbOfTxs>1</NbOfTxs>` +
		`<SttlmInf><SttlmMtd>CLRG</SttlmMtd></SttlmInf><PmtTpInf><InstrPrty>HIGH</InstrPrty><SvcLvl><Prtry>PAGPRI</Prtry></SvcLvl></PmtTpInf></GrpHdr>` +
		`<CdtTrfTxInf><PmtId><EndToEndId>` + e2e + `</EndToEndId></PmtId><IntrBkSttlmAmt Ccy="BRL">` + amount + `</IntrBkSttlmAmt>` +
		`<AccptncDtTm>` + stamp(time.Now()) + `</AccptncDtTm><ChrgBr>SLEV</ChrgBr>` +
		`<MndtRltdInf><Tp><LclInstrm><Prtry>DICT</Prtry></LclInstrm></Tp></MndtRltdInf>` +
		`<Dbtr><Nm>` + from.name + `</Nm><Id><PrvtId><Othr><Id>` + from.taxID + `</Id></Othr></PrvtId></Id></Dbtr>` +
		`<DbtrAcct><Id><Othr><Id>` + from.account + `</Id><Issr>` + from.branch + `</Issr></Othr></Id><Tp><Cd>` + from.accountType + `</Cd></Tp></DbtrAcct>` +
		agent("DbtrAgt", from.ispb) + agent("CdtrAgt", to.Account.Participant) +
		`<Cdtr><Id><PrvtId><Othr><Id>` + to.Owner.TaxIDNumber + `</Id></Othr></PrvtId></Id></Cdtr>` +
		`<CdtrAcct><Id><Othr><Id>` + to.Account.AccountNumber + `</Id><Issr>` + to.Account.Branch + `</Issr></Othr></Id>` +
		`<Tp><Cd>` + to.Account.AccountType + `</Cd></Tp><Prxy><Id>` + key + `</Id></Prxy></CdtrAcct>` +
		`<Purp><Cd>IPAY</Cd></Purp></CdtTrfTxInf></FIToFICstmrCdtTrf></Document></Envelope>`
}

// pacs002 is the answer of the receiving participant to a payment or a return (instr is the EndToEndId or the RtrId).
func pacs002(from, instr, e2e, status string) string {
	m := msgID(from)
	return `<?xml version="1.0" encoding="UTF-8"?><Envelope xmlns="https://www.bcb.gov.br/pi/pacs.002/1.18">` +
		header(from, "00038166", m, "pacs.002.spi.1.18") +
		`<Document><FIToFIPmtStsRpt><GrpHdr><MsgId>` + m + `</MsgId><CreDtTm>` + stamp(time.Now()) + `</CreDtTm></GrpHdr>` +
		`<TxInfAndSts><OrgnlInstrId>` + instr + `</OrgnlInstrId><OrgnlEndToEndId>` + e2e + `</OrgnlEndToEndId><TxSts>` + status + `</TxSts></TxInfAndSts>` +
		`</FIToFIPmtStsRpt></Document></Envelope>`
}

// pacs004 returns a payment: from is the participant that received it, to the one that paid it. The reason is MD06,
// or BE08 and FR01 for the MED.
func pacs004(from, to, rtr, e2e, amount, reason string) string {
	m := msgID(from)
	return `<?xml version="1.0" encoding="UTF-8"?><Envelope xmlns="https://www.bcb.gov.br/pi/pacs.004/1.5">` +
		header(from, "00038166", m, "pacs.004.spi.1.5") +
		`<Document><PmtRtr><GrpHdr><MsgId>` + m + `</MsgId><CreDtTm>` + stamp(time.Now()) + `</CreDtTm><NbOfTxs>1</NbOfTxs>` +
		`<SttlmInf><SttlmMtd>CLRG</SttlmMtd></SttlmInf></GrpHdr>` +
		`<TxInf><RtrId>` + rtr + `</RtrId><OrgnlEndToEndId>` + e2e + `</OrgnlEndToEndId><RtrdIntrBkSttlmAmt Ccy="BRL">` + amount + `</RtrdIntrBkSttlmAmt>` +
		`<SttlmPrty>HIGH</SttlmPrty><ChrgBr>SLEV</ChrgBr><RtrRsnInf><Rsn><Cd>` + reason + `</Cd></Rsn></RtrRsnInf>` +
		`<OrgnlTxRef>` + agent("DbtrAgt", from) + agent("CdtrAgt", to) + `</OrgnlTxRef></TxInf></PmtRtr></Document></Envelope>`
}

// trck002 reports a payment between two customers of the same participant, settled five minutes ago.
func trck002(ispb, e2e, amount string) string {
	m := msgID(ispb)
	return `<?xml version="1.0" encoding="UTF-8"?><Envelope xmlns="https://www.bcb.gov.br/pi/trck.002/1.1">` +
		header(ispb, "00038166", m, "trck.002.spi.1.1") +
		`<Document><PmtStsTrckrRpt><GrpHdr><MsgId>` + m + `</MsgId><CreDtTm>` + stamp(time.Now()) + `</CreDtTm></GrpHdr>` +
		`<TrckrStsAndTx><TxSts><Sts>ACCC</Sts></TxSts><Tx><PmtId><EndToEndId>` + e2e + `</EndToEndId></PmtId>` +
		`<PmtTpInf><LclInstrm><Prtry>MANU</Prtry></LclInstrm></PmtTpInf><PmtScnro><Prtry>BOK1</Prtry></PmtScnro>` +
		`<IntrBkSttlmAmt Ccy="BRL">` + amount + `</IntrBkSttlmAmt><ReqdExctnDt><DtTm>` + stamp(time.Now().Add(-5*time.Minute)) + `</DtTm></ReqdExctnDt>` +
		`<Dbtr><Pty><Id><PrvtId><Othr><Id>` + anaCPF + `</Id></Othr></PrvtId></Id></Pty></Dbtr>` +
		`<DbtrAcct><Id><Othr><Id>00123456</Id><Issr>0001</Issr></Othr></Id><Tp><Cd>CACC</Cd></Tp></DbtrAcct>` +
		agent("DbtrAgt", ispb) + agent("CdtrAgt", ispb) +
		`<Cdtr><Pty><Id><PrvtId><Othr><Id>` + brunoCPF + `</Id></Othr></PrvtId></Id></Pty></Cdtr>` +
		`<CdtrAcct><Id><Othr><Id>00234567</Id><Issr>0001</Issr></Othr></Id><Tp><Cd>SVGS</Cd></Tp></CdtrAcct>` +
		`</Tx></TrckrStsAndTx></PmtStsTrckrRpt></Document></Envelope>`
}

// --- SPI: receiving ---

// message is a message read from a participant's output stream.
type message struct {
	kind string // MsgDefIdr, such as "pacs.002.spi.1.18"
	body []byte
}

var msgDefIdr = regexp.MustCompile(`<MsgDefIdr>([^<]+)</MsgDefIdr>`)

// waitFor reads the participant's output until a message satisfies match, acknowledging everything it reads
// along the way, and fails the test if none arrives in time.
func (e *env) waitFor(ispb, what string, match func(m message) bool) message {
	e.t.Helper()
	deadline := time.Now().Add(e.deadline)
	for time.Now().Before(deadline) {
		resp, err := http.Get(e.spiURL + "/api/v1/out/" + ispb + "/stream/start")
		if err != nil {
			e.t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			continue // nothing yet: the read already waited for a message
		}
		if resp.StatusCode != http.StatusOK {
			e.t.Fatalf("read as %s = %d: %s", ispb, resp.StatusCode, body)
		}
		// Acknowledge, so the same message is not delivered again.
		req, _ := http.NewRequest(http.MethodDelete, e.spiURL+resp.Header.Get("PI-Pull-Next"), nil)
		if del, err := http.DefaultClient.Do(req); err == nil {
			del.Body.Close()
		}
		m := message{body: body}
		if g := msgDefIdr.FindSubmatch(body); g != nil {
			m.kind = string(g[1])
		}
		if match(m) {
			return m
		}
	}
	e.t.Fatalf("%s: waited %v for %s and it did not arrive", ispb, e.deadline, what)
	return message{}
}

// status is a pacs.002 transaction status.
type status struct {
	instr, e2e, status, reason string
}

// waitForStatus waits for a pacs.002 about the payment or return with the given instruction id.
func (e *env) waitForStatus(ispb, instr string) status {
	e.t.Helper()
	var st status
	e.waitFor(ispb, "a pacs.002 for "+instr, func(m message) bool {
		if !strings.HasPrefix(m.kind, "pacs.002") {
			return false
		}
		var p struct {
			Tx []struct {
				Instr  string `xml:"OrgnlInstrId"`
				E2E    string `xml:"OrgnlEndToEndId"`
				Status string `xml:"TxSts"`
				Reason string `xml:"StsRsnInf>Rsn>Cd"`
			} `xml:"Document>FIToFIPmtStsRpt>TxInfAndSts"`
		}
		if xml.Unmarshal(m.body, &p) != nil {
			return false
		}
		for _, tx := range p.Tx {
			if tx.Instr == instr {
				st = status{tx.Instr, tx.E2E, tx.Status, tx.Reason}
				return true
			}
		}
		return false
	})
	return st
}

// waitForOrder waits for the pacs.008 or pacs.004 the SPI forwards to a participant.
func (e *env) waitForOrder(ispb, family, instr string) message {
	e.t.Helper()
	return e.waitFor(ispb, family+" "+instr, func(m message) bool {
		return strings.HasPrefix(m.kind, family) && bytes.Contains(m.body, []byte(instr))
	})
}

func (e *env) expect(got status, instr, want, reason string) {
	e.t.Helper()
	if got.instr != instr || got.status != want || got.reason != reason {
		e.t.Fatalf("status of %s = %+v; want %s %s", instr, got, want, reason)
	}
}

// --- The scenarios ---

var (
	ana   = payer{wallet, "0001", "00123456", "CACC", anaCPF, "Ana Souza"}
	carlo = payer{alfa, "0001", "00456789", "CACC", carlosCPF, "Carlos Mendes"}
)

// payKey does what a payer's participant does: create the EndToEndId, look the key up in the DICT with it, then
// send the payment to the SPI with the same EndToEndId. It returns the EndToEndId and the entry the DICT gave.
func (e *env) payKey(from payer, key, amount string) (string, entry) {
	e.t.Helper()
	e2e := id("E", from.ispb)
	code, ent, problem := e.lookup(from.ispb, from.taxID, e2e, key)
	if code != http.StatusOK {
		e.t.Fatalf("DICT lookup of %s = %d %s", key, code, problem)
	}
	e.send(from.ispb, pacs008(from, ent, key, e2e, amount))
	return e2e, ent
}

// The path of the roadmap's "Done when": look up a seeded key in the DICT, pay it through the SPI and get the settled
// confirmation; get a forced rejection; return the payment; report an internal transaction.
func TestPixEndToEnd(t *testing.T) {
	e := newEnv(t)

	var paid string // the payment the return scenario sends back
	const amount = "12.34"

	t.Run("pay a key of another bank", func(t *testing.T) {
		e2e, ent := e.payKey(ana, carlosCPF, amount)
		if ent.Account.Participant != alfa || ent.Account.AccountNumber != "00456789" || ent.Owner.Name != "Carlos Mendes" {
			t.Fatalf("DICT returned %+v for Carlos Mendes' CPF", ent)
		}
		e.expect(e.waitForStatus(wallet, e2e), e2e, "ACSC", "") // the payer's participant is told it settled
		e.expect(e.waitForStatus(alfa, e2e), e2e, "ACCC", "")   // so is the receiver's
		paid = e2e
	})

	t.Run("a closed account rejects the payment", func(t *testing.T) {
		e2e, _ := e.payKey(ana, eduardoCPF, "5.00")
		e.expect(e.waitForStatus(wallet, e2e), e2e, "RJCT", "AC07")
	})

	t.Run("a blocked account rejects the payment", func(t *testing.T) {
		e2e, _ := e.payKey(ana, fernandaCPF, "5.00")
		e.expect(e.waitForStatus(wallet, e2e), e2e, "RJCT", "AC06")
	})

	t.Run("each test account rejects with its own reason", func(t *testing.T) {
		// Test accounts 00900004 to 00900011: the holder's CPF is the key, and the account rejects every payment.
		for _, c := range []struct{ cpf, code string }{
			{"10293847541", "DS04"}, {"56473829164", "FRAD"}, {"91827364564", "AM02"}, {"37281946546", "AG03"},
			{"64528193728", "ED05"}, {"82910476340", "AB09"}, {"45197286300", "AC14"}, {"73640812913", "CH11"},
		} {
			e2e, _ := e.payKey(ana, c.cpf, "5.00")
			e.expect(e.waitForStatus(wallet, e2e), e2e, "RJCT", c.code)
		}
	})

	t.Run("an unknown key is not found in the DICT", func(t *testing.T) {
		code, _, problem := e.lookup(wallet, anaCPF, id("E", wallet), "00000000191")
		if code != http.StatusNotFound {
			t.Fatalf("lookup of a key nobody holds = %d %s; want 404", code, problem)
		}
	})

	t.Run("receive a payment from another bank", func(t *testing.T) {
		// Carlos Mendes (Banco Alfa) pays Ana Souza's key. The wallet gets the order, accepts it, and both are told.
		e2e, _ := e.payKey(carlo, anaCPF, "7.00")
		order := e.waitForOrder(wallet, "pacs.008", e2e)
		if !bytes.Contains(order.body, []byte(">7.00<")) {
			t.Fatalf("forwarded order has not the amount: %s", order.body)
		}
		e.send(wallet, pacs002(wallet, e2e, e2e, "ACSP"))
		e.expect(e.waitForStatus(wallet, e2e), e2e, "ACCC", "")
		e.expect(e.waitForStatus(alfa, e2e), e2e, "ACSC", "")
	})

	t.Run("return the first payment", func(t *testing.T) {
		if paid == "" {
			t.Skip("the first payment did not settle")
		}
		// Banco Alfa received the payment from the wallet and sends the money back. The wallet gets the return and accepts it.
		rtr := id("D", alfa)
		e.send(alfa, pacs004(alfa, wallet, rtr, paid, amount, "MD06"))
		e.waitForOrder(wallet, "pacs.004", rtr)
		e.send(wallet, pacs002(wallet, rtr, paid, "ACSP"))
		e.expect(e.waitForStatus(wallet, rtr), rtr, "ACCC", "")
		if got := e.waitForStatus(alfa, rtr); got.instr != rtr || got.e2e != paid || got.status != "ACSC" {
			t.Fatalf("return confirmation to Banco Alfa = %+v; want ACSC for %s of payment %s", got, rtr, paid)
		}
	})

	t.Run("a payment between two wallet customers stays inside the wallet", func(t *testing.T) {
		// The DICT refuses to resolve a key held by the participant asking: it is a book transfer.
		code, _, problem := e.lookup(wallet, anaCPF, id("E", wallet), brunoCPF)
		if code != http.StatusBadRequest || problem != "EntryCannotBeQueriedForBookTransfer" {
			t.Fatalf("lookup of a key of the same participant = %d %s; want 400 EntryCannotBeQueriedForBookTransfer", code, problem)
		}

		// The wallet settles it in its own ledger and reports it to the SPI, which records it and acknowledges.
		e2e := id("E", wallet)
		e.send(wallet, trck002(wallet, e2e, "3.50"))
		m := e.waitFor(wallet, "a camt.025 for "+e2e, func(m message) bool {
			return strings.HasPrefix(m.kind, "camt.025") && bytes.Contains(m.body, []byte(e2e))
		})
		var c struct {
			Status string `xml:"Document>Rct>RctDtls>ReqHdlg>Sts>Cd"`
		}
		if err := xml.Unmarshal(m.body, &c); err != nil || c.Status != "ACPT" {
			t.Fatalf("receipt for the report = %q (%v); want ACPT\n%s", c.Status, err, m.body)
		}
	})

	t.Run("a payment the bank never answers is rejected by timeout", func(t *testing.T) {
		timeout := os.Getenv("PIX_E2E_TIMEOUT")
		if timeout == "" {
			t.Skip("PIX_E2E_TIMEOUT is not set: the SPI would take its full 40 seconds")
		}
		d, err := time.ParseDuration(timeout)
		if err != nil {
			t.Fatalf("PIX_E2E_TIMEOUT = %q: %v", timeout, err)
		}
		e.deadline = d + 20*time.Second
		e2e, _ := e.payKey(ana, gustavoCPF, "5.00")
		e.expect(e.waitForStatus(wallet, e2e), e2e, "RJCT", "AB03")
	})

	t.Run("an invalid message gets an error notification", func(t *testing.T) {
		e.send(wallet, `<not-iso/>`)
		e.waitFor(wallet, "an admi.002", func(m message) bool { return strings.HasPrefix(m.kind, "admi.002") })
	})
}
