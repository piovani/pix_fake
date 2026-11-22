package pix

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 19, 15, 30, 0, 0, time.UTC)

func testOrder() PaymentOrder {
	return PaymentOrder{
		EndToEndID:  "E99000001202611191530a1B2c3D4e5F",
		AmountCents: 2590,
		Payer:       Party{ISPB: "99000001", Name: "Ana Souza", TaxID: "52998224725", Branch: "0001", Account: "00123456", AccountType: AccountChecking},
		Receiver:    Party{ISPB: "99000002", TaxID: "39053344705", Branch: "0001", Account: "00456789", AccountType: AccountChecking},
		Key:         "39053344705",
		Message:     "lunch",
	}
}

func TestPacs008BuildAndParse(t *testing.T) {
	msgID, body, err := NewPacs008("99000001", testOrder(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgID) != 32 || msgID[:9] != "M99000001" {
		t.Fatalf("msgID = %q", msgID)
	}
	xmlText := string(body)
	for _, want := range []string{
		`<Envelope xmlns="https://www.bcb.gov.br/pi/pacs.008/1.16">`,
		`<Id>00038166</Id>`,
		`<MsgDefIdr>pacs.008.spi.1.16</MsgDefIdr>`,
		`<SttlmMtd>CLRG</SttlmMtd>`,
		`<Prtry>PAGPRI</Prtry>`,
		`<IntrBkSttlmAmt Ccy="BRL">25.90</IntrBkSttlmAmt>`,
		`<EndToEndId>E99000001202611191530a1B2c3D4e5F</EndToEndId>`,
		`<Prtry>DICT</Prtry>`,
		`<Prxy>`,
		`<Cd>IPAY</Cd>`,
		`<CreDtTm>2026-11-19T15:30:00.000Z</CreDtTm>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Errorf("missing %s in:\n%s", want, xmlText)
		}
	}

	kind, err := MessageKind(body)
	if err != nil || kind != KindPacs008 {
		t.Fatalf("kind = %q, err = %v", kind, err)
	}
	got, err := ParsePacs008(body)
	if err != nil || len(got) != 1 {
		t.Fatalf("parse: %+v, %v", got, err)
	}
	want := testOrder()
	want.MsgID, want.AcceptedAt = msgID, testNow
	if got[0] != want {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got[0], want)
	}
}

func TestPacs008Manual(t *testing.T) {
	o := testOrder()
	o.Key, o.Message = "", ""
	_, body, err := NewPacs008("99000001", o, testNow)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `<Prtry>MANU</Prtry>`) || strings.Contains(s, "Prxy") || strings.Contains(s, "RmtInf") {
		t.Fatalf("manual payment:\n%s", s)
	}
}

func TestPacs008Validation(t *testing.T) {
	o := testOrder()
	o.AmountCents = 0
	if _, _, err := NewPacs008("99000001", o, testNow); !errors.Is(err, ErrBadRequest) {
		t.Errorf("zero amount: %v", err)
	}
	o = testOrder()
	o.EndToEndID = ""
	if _, _, err := NewPacs008("99000001", o, testNow); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no EndToEndId: %v", err)
	}
	if _, _, err := NewPacs008("123", testOrder(), testNow); err == nil {
		t.Error("bad ISPB accepted")
	}
}

func TestPacs002BuildAndParse(t *testing.T) {
	e2e := "E99000002202611191530a1B2c3D4e5F"

	_, body, err := NewPacs002("99000001", TxStatus{EndToEndID: e2e, Status: StatusAccepted}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParsePacs002(body)
	if err != nil || len(st) != 1 || st[0].Status != StatusAccepted || st[0].InstrID != e2e || st[0].EndToEndID != e2e || st[0].Rejected() {
		t.Fatalf("accept: %+v, %v", st, err)
	}

	_, body, err = NewPacs002("99000001", TxStatus{EndToEndID: e2e, Status: StatusRejected, ReasonCode: "AC03", Detail: "no such account"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	st, _ = ParsePacs002(body)
	if len(st) != 1 || !st[0].Rejected() || st[0].ReasonCode != "AC03" || st[0].Detail != "no such account" {
		t.Fatalf("reject: %+v", st)
	}
	if !strings.Contains(string(body), "<Cd>AC03</Cd>") {
		t.Errorf("reason code not in the XML:\n%s", body)
	}

	if _, _, err := NewPacs002("99000001", TxStatus{EndToEndID: e2e, Status: StatusRejected}, testNow); !errors.Is(err, ErrBadRequest) {
		t.Errorf("rejection without reason: %v", err)
	}
}

// A settled payment as the SPI sends it, with the settlement date.
const settledFromSPI = `<?xml version="1.0" encoding="UTF-8"?>
<Envelope xmlns="https://www.bcb.gov.br/pi/pacs.002/1.18">
  <AppHdr><Fr><FIId><FinInstnId><Othr><Id>00038166</Id></Othr></FinInstnId></FIId></Fr>
  <To><FIId><FinInstnId><Othr><Id>99000001</Id></Othr></FinInstnId></FIId></To>
  <BizMsgIdr>M00038166abcdefghijklmnopqrstuvw</BizMsgIdr><MsgDefIdr>pacs.002.spi.1.18</MsgDefIdr><CreDt>2026-11-19T15:30:01.000Z</CreDt><Sgntr/></AppHdr>
  <Document><FIToFIPmtStsRpt>
    <GrpHdr><MsgId>M00038166abcdefghijklmnopqrstuvw</MsgId><CreDtTm>2026-11-19T15:30:01.000Z</CreDtTm></GrpHdr>
    <TxInfAndSts>
      <OrgnlInstrId>E99000001202611191530a1B2c3D4e5F</OrgnlInstrId>
      <OrgnlEndToEndId>E99000001202611191530a1B2c3D4e5F</OrgnlEndToEndId>
      <TxSts>ACSC</TxSts>
      <FctvIntrBkSttlmDt><DtTm>2026-11-19T15:30:01.000Z</DtTm></FctvIntrBkSttlmDt>
      <OrgnlTxRef><IntrBkSttlmDt>2026-11-19</IntrBkSttlmDt></OrgnlTxRef>
    </TxInfAndSts>
  </FIToFIPmtStsRpt></Document>
</Envelope>`

func TestParsePacs002(t *testing.T) {
	st, err := ParsePacs002([]byte(settledFromSPI))
	if err != nil || len(st) != 1 {
		t.Fatalf("%+v, %v", st, err)
	}
	if st[0].Status != StatusSettled || st[0].EndToEndID != "E99000001202611191530a1B2c3D4e5F" || !st[0].SettledAt.Equal(time.Date(2026, 11, 19, 15, 30, 1, 0, time.UTC)) {
		t.Fatalf("status: %+v", st[0])
	}
	if kind, _ := MessageKind([]byte(settledFromSPI)); kind != KindPacs002 {
		t.Fatalf("kind = %q", kind)
	}
}

func TestParseAdmi002(t *testing.T) {
	body := `<Envelope xmlns="https://www.bcb.gov.br/pi/admi.002/1.5"><AppHdr><MsgDefIdr>admi.002.spi.1.5</MsgDefIdr></AppHdr>
	<Document><admi.002.001.01><RltdRef><Ref>res123</Ref></RltdRef><Rsn><RjctgPtyRsn>Invalid XML</RjctgPtyRsn><RsnDesc>line 3</RsnDesc></Rsn></admi.002.001.01></Document></Envelope>`
	n, err := ParseAdmi002([]byte(body))
	if err != nil || n.Ref != "res123" || n.Reason != "Invalid XML" || n.Description != "line 3" {
		t.Fatalf("%+v, %v", n, err)
	}
	if kind, _ := MessageKind([]byte(body)); kind != KindAdmi002 {
		t.Fatalf("kind = %q", kind)
	}
}

func TestMessageKindErrors(t *testing.T) {
	if _, err := MessageKind([]byte("not xml")); err == nil {
		t.Error("not XML accepted")
	}
	if _, err := MessageKind([]byte("<Envelope><AppHdr></AppHdr></Envelope>")); err == nil {
		t.Error("message without MsgDefIdr accepted")
	}
}

func TestAmounts(t *testing.T) {
	for in, want := range map[string]int64{"25.90": 2590, "25.9": 2590, "25": 2500, "0.01": 1, "1000000.00": 100000000} {
		got, err := parseAmount(in)
		if err != nil || got != want {
			t.Errorf("parseAmount(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1.234", "-1.00", "1,50"} {
		if _, err := parseAmount(in); err == nil {
			t.Errorf("parseAmount(%q) was accepted", in)
		}
	}
	if formatAmount(5) != "0.05" || formatAmount(2590) != "25.90" || formatAmount(100000) != "1000.00" {
		t.Errorf("formatAmount: %s %s %s", formatAmount(5), formatAmount(2590), formatAmount(100000))
	}
}

func TestNewMessageID(t *testing.T) {
	id, err := NewMessageID("99000001")
	if err != nil || len(id) != 32 || id[:9] != "M99000001" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	if _, err := NewMessageID("short"); err == nil {
		t.Error("bad ISPB accepted")
	}
}
