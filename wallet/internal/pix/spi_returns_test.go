package pix

import (
	"encoding/xml"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

var betweenTags = regexp.MustCompile(`>\s+<`)

// compact removes the indentation so a test can look for a run of tags.
func compact(b []byte) string { return betweenTags.ReplaceAllString(string(b), "><") }

const (
	testReturnID = "D99000001202611191545a1B2c3D4e5F"
	testE2E      = "E99000002202611191530a1B2c3D4e5F"
)

func testReturn() PaymentReturn {
	return PaymentReturn{ReturnID: testReturnID, EndToEndID: testE2E, AmountCents: 1050, To: "99000002", Detail: "paid twice", Message: "sorry"}
}

func TestPacs004BuildAndParse(t *testing.T) {
	msgID, body, err := NewPacs004("99000001", testReturn(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgID) != 32 || msgID[:9] != "M99000001" {
		t.Fatalf("msgID = %q", msgID)
	}
	s := compact(body)
	for _, want := range []string{
		`<Envelope xmlns="https://www.bcb.gov.br/pi/pacs.004/1.5">`,
		`<MsgDefIdr>pacs.004.spi.1.5</MsgDefIdr>`,
		`<SttlmMtd>CLRG</SttlmMtd>`,
		`<RtrId>` + testReturnID + `</RtrId>`,
		`<OrgnlEndToEndId>` + testE2E + `</OrgnlEndToEndId>`,
		`<RtrdIntrBkSttlmAmt Ccy="BRL">10.50</RtrdIntrBkSttlmAmt>`,
		`<SttlmPrty>HIGH</SttlmPrty>`,
		`<Cd>MD06</Cd>`,
		`<AddtlInf>paid twice</AddtlInf>`,
		`<Ustrd>sorry</Ustrd>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
	// the sender is the debtor agent, the payer of the original payment the creditor agent
	if !strings.Contains(s, "<DbtrAgt><FinInstnId><ClrSysMmbId><MmbId>99000001</MmbId>") ||
		!strings.Contains(s, "<CdtrAgt><FinInstnId><ClrSysMmbId><MmbId>99000002</MmbId>") {
		t.Errorf("agents:\n%s", s)
	}
	if kind, err := MessageKind(body); err != nil || kind != KindPacs004 {
		t.Fatalf("kind = %q, err = %v", kind, err)
	}

	got, err := ParsePacs004(body)
	if err != nil || len(got) != 1 {
		t.Fatalf("parse: %+v, %v", got, err)
	}
	want := testReturn()
	want.MsgID, want.ReasonCode, want.From = msgID, "MD06", "99000001"
	if got[0] != want {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got[0], want)
	}
}

func TestPacs004WithoutOptionalText(t *testing.T) {
	r := testReturn()
	r.Detail, r.Message = "", ""
	_, body, err := NewPacs004("99000001", r, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(body); strings.Contains(s, "RmtInf") || strings.Contains(s, "AddtlInf") {
		t.Fatalf("empty text written:\n%s", s)
	}
}

func TestPacs004Validation(t *testing.T) {
	cases := map[string]func(*PaymentReturn){
		"zero amount":    func(r *PaymentReturn) { r.AmountCents = 0 },
		"bad RtrId":      func(r *PaymentReturn) { r.ReturnID = testE2E }, // an E..., not a D...
		"no RtrId":       func(r *PaymentReturn) { r.ReturnID = "" },
		"bad EndToEndId": func(r *PaymentReturn) { r.EndToEndID = "E123" },
		"no receiver":    func(r *PaymentReturn) { r.To = "" },
		"same ISPB":      func(r *PaymentReturn) { r.To = "99000001" },
	}
	for name, mutate := range cases {
		r := testReturn()
		mutate(&r)
		if _, _, err := NewPacs004("99000001", r, testNow); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNewReturnID(t *testing.T) {
	now := time.Date(2026, 11, 19, 15, 30, 59, 0, time.FixedZone("BRT", -3*3600))
	id, err := NewReturnID("99000001", now)
	if err != nil || len(id) != 32 || id[:21] != "D99000001202611191830" || !returnIDRe.MatchString(id) {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	if other, _ := NewReturnID("99000001", now); other == id {
		t.Fatal("two identifiers are equal")
	}
	for _, ispb := range []string{"", "1234567", "abcdefgh"} {
		if _, err := NewReturnID(ispb, now); err == nil {
			t.Errorf("ISPB %q was accepted", ispb)
		}
	}
}

// The pacs.002 that answers a return names it by its RtrId.
func TestPacs002AnswersAReturnByItsRtrID(t *testing.T) {
	_, body, err := NewPacs002("99000001", TxStatus{InstrID: testReturnID, EndToEndID: testE2E, Status: StatusAccepted}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := ParsePacs002(body)
	if len(st) != 1 || st[0].InstrID != testReturnID || st[0].EndToEndID != testE2E {
		t.Fatalf("%+v", st)
	}
}

func testReported() ReportedTx {
	return ReportedTx{
		EndToEndID:  "E99000001202611191530a1B2c3D4e5F",
		AmountCents: 2590,
		SettledAt:   testNow.Add(-time.Minute),
		Payer:       Party{ISPB: "99000001", TaxID: "52998224725", Branch: "0001", Account: "00123456", AccountType: AccountChecking},
		Receiver:    Party{ISPB: "99000001", TaxID: "39053344705", Branch: "0001", Account: "00456789", AccountType: AccountChecking},
		Key:         "39053344705",
	}
}

func TestTrck002BuildBookTransfer(t *testing.T) {
	msgID, body, err := NewTrck002("99000001", []ReportedTx{testReported()}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgID) != 32 || msgID[:9] != "M99000001" {
		t.Fatalf("msgID = %q", msgID)
	}
	s := compact(body)
	for _, want := range []string{
		`<Envelope xmlns="https://www.bcb.gov.br/pi/trck.002/1.1">`,
		`<MsgDefIdr>trck.002.spi.1.1</MsgDefIdr>`,
		`<MsgId>` + msgID + `</MsgId>`,
		`<Sts>ACCC</Sts>`,
		`<EndToEndId>E99000001202611191530a1B2c3D4e5F</EndToEndId>`,
		`<Prtry>DICT</Prtry>`,
		`<Prtry>BOK1</Prtry>`,
		`<IntrBkSttlmAmt Ccy="BRL">25.90</IntrBkSttlmAmt>`,
		`<DtTm>2026-11-19T15:29:00.000Z</DtTm>`,
		`<Dbtr><Pty><Id><PrvtId><Othr><Id>52998224725</Id>`,
		`<Cdtr><Pty><Id><PrvtId><Othr><Id>39053344705</Id>`,
		`<Prxy><Id>39053344705</Id></Prxy>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "InstrId") {
		t.Errorf("InstrId written for a payment that is not a return:\n%s", s)
	}
	if kind, err := MessageKind(body); err != nil || kind != "trck.002" {
		t.Fatalf("kind = %q, err = %v", kind, err)
	}
	var back trck002XML
	if err := xml.Unmarshal(body, &back); err != nil || len(back.Document.PmtStsTrckrRpt.TrckrStsAndTx.Tx) != 1 {
		t.Fatalf("not readable: %v", err)
	}
}

func TestTrck002ManualPaymentHasNoKey(t *testing.T) {
	tx := testReported()
	tx.Key = ""
	_, body, err := NewTrck002("99000001", []ReportedTx{tx}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(body); !strings.Contains(s, "<Prtry>MANU</Prtry>") || strings.Contains(s, "Prxy") {
		t.Fatalf("manual payment:\n%s", s)
	}
}

func TestTrck002Return(t *testing.T) {
	tx := testReported()
	tx.Key, tx.ReturnID = "", testReturnID
	_, body, err := NewTrck002("99000001", []ReportedTx{tx}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "<InstrId>"+testReturnID+"</InstrId>") || !strings.Contains(s, "<Prtry>MANU</Prtry>") {
		t.Fatalf("return:\n%s", s)
	}
	tx.Initiation, tx.Key = "DICT", "39053344705"
	if _, _, err := NewTrck002("99000001", []ReportedTx{tx}, testNow); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a return initiated as DICT: %v", err)
	}
}

func TestTrck002Validation(t *testing.T) {
	cases := map[string]func(*ReportedTx){
		"zero amount":             func(x *ReportedTx) { x.AmountCents = 0 },
		"bad EndToEndId":          func(x *ReportedTx) { x.EndToEndID = "nope" },
		"bad RtrId":               func(x *ReportedTx) { x.ReturnID = "D1" },
		"salary account":          func(x *ReportedTx) { x.Receiver.AccountType = AccountSalary },
		"in the future":           func(x *ReportedTx) { x.SettledAt = testNow.Add(time.Minute) },
		"no settlement time":      func(x *ReportedTx) { x.SettledAt = time.Time{} },
		"too old":                 func(x *ReportedTx) { x.SettledAt = testNow.Add(-31 * 24 * time.Hour) },
		"unknown initiation":      func(x *ReportedTx) { x.Initiation = "WHAT" },
		"MANU with a key":         func(x *ReportedTx) { x.Initiation = "MANU" },
		"DICT without a key":      func(x *ReportedTx) { x.Key = ""; x.Initiation = "DICT" },
		"no tax ID":               func(x *ReportedTx) { x.Payer.TaxID = "" },
		"other bank, no scenario": func(x *ReportedTx) { x.Receiver.ISPB = "99000002" },
	}
	for name, mutate := range cases {
		tx := testReported()
		mutate(&tx)
		if _, _, err := NewTrck002("99000001", []ReportedTx{tx}, testNow); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// the same participants with the scenario stated are accepted
	tx := testReported()
	tx.Receiver.ISPB, tx.Scenario = "99000002", ScenarioSameSettler
	_, body, err := NewTrck002("99000001", []ReportedTx{tx}, testNow)
	if err != nil || !strings.Contains(string(body), "<Prtry>BOK2</Prtry>") {
		t.Errorf("BOK2: %v", err)
	}
}

func TestTrck002Limits(t *testing.T) {
	if _, _, err := NewTrck002("99000001", nil, testNow); !errors.Is(err, ErrBadRequest) {
		t.Errorf("empty report: %v", err)
	}
	many := make([]ReportedTx, maxReportedTxs)
	for i := range many {
		many[i] = testReported()
	}
	if _, _, err := NewTrck002("99000001", many, testNow); err != nil {
		t.Errorf("%d transactions: %v", maxReportedTxs, err)
	}
	if _, _, err := NewTrck002("99000001", append(many, testReported()), testNow); !errors.Is(err, ErrBadRequest) {
		t.Errorf("%d transactions: %v", maxReportedTxs+1, err)
	}
}

// A receipt as the SPI sends it: one accepted transaction and one rejected.
const camt025FromSPI = `<?xml version="1.0" encoding="UTF-8"?>
<Envelope xmlns="https://www.bcb.gov.br/pi/camt.025/1.0">
  <AppHdr><MsgDefIdr>camt.025.spi.1.0</MsgDefIdr></AppHdr>
  <Document><Rct>
    <MsgHdr><MsgId>M00038166abcdefghijklmnopqrstuvw</MsgId><CreDtTm>2026-11-19T15:30:01.000Z</CreDtTm></MsgHdr>
    <RctDtls>
      <OrgnlMsgId><MsgId>M99000001reportreportreportrep001</MsgId></OrgnlMsgId>
      <OrgnlPmtId><PrtryId>E99000001202611191530a1B2c3D4e5F</PrtryId></OrgnlPmtId>
      <ReqHdlg><Sts><Cd>ACPT</Cd></Sts></ReqHdlg>
    </RctDtls>
    <RctDtls>
      <OrgnlMsgId><MsgId>M99000001reportreportreportrep001</MsgId></OrgnlMsgId>
      <OrgnlPmtId><PrtryId>D99000001202611191545a1B2c3D4e5F</PrtryId></OrgnlPmtId>
      <ReqHdlg><Sts><Cd>RJCT</Cd></Sts><StsRsn><Rsn><Prtry>FF08</Prtry></Rsn><AddtlInf>too late</AddtlInf></StsRsn></ReqHdlg>
    </RctDtls>
  </Rct></Document>
</Envelope>`

func TestParseCamt025(t *testing.T) {
	rs, err := ParseCamt025([]byte(camt025FromSPI))
	if err != nil || len(rs) != 2 {
		t.Fatalf("%+v, %v", rs, err)
	}
	if rs[0].Rejected() || rs[0].Status != ReceiptAccepted || rs[0].Ref != "E99000001202611191530a1B2c3D4e5F" || rs[0].ReportMsgID != "M99000001reportreportreportrep001" || rs[0].ReasonCode != "" {
		t.Errorf("accepted: %+v", rs[0])
	}
	if !rs[1].Rejected() || rs[1].Ref != "D99000001202611191545a1B2c3D4e5F" || rs[1].ReasonCode != "FF08" || rs[1].Detail != "too late" {
		t.Errorf("rejected: %+v", rs[1])
	}
	if kind, _ := MessageKind([]byte(camt025FromSPI)); kind != KindCamt025 {
		t.Fatalf("kind = %q", kind)
	}
	if _, err := ParseCamt025([]byte("not xml")); err == nil {
		t.Error("not XML accepted")
	}
}
