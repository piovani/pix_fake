package iso_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pixfake/mock/spi/internal/iso"
)

const pkg = "../../docs/catalog/spi.5.13.2"

func buildPacs008() *iso.Pacs008 {
	tx := iso.NewCreditTransfer("E9900000120261114120000000000001", "123.45", "2026-11-14T12:00:00.000Z")
	tx.SetDebtor("Ana Souza", "52998224725", "99000001", "0001", "00123456", "CACC")
	tx.SetCreditor("39053344705", "99000002", "0001", "00456789", "CACC", "39053344705")
	return iso.NewPacs008("00038166", "99000002", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:00.000Z", "HIGH", "PAGPRI", tx)
}

func TestPacs008RoundTrip(t *testing.T) {
	body, err := buildPacs008().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `xmlns="https://www.bcb.gov.br/pi/pacs.008/1.16"`) {
		t.Fatalf("namespace missing:\n%s", body)
	}
	got, err := iso.ParsePacs008(body)
	if err != nil {
		t.Fatal(err)
	}
	txs := got.Transactions()
	if len(txs) != 1 {
		t.Fatalf("transactions = %d", len(txs))
	}
	tx := txs[0]
	if tx.PmtID.EndToEnd != "E9900000120261114120000000000001" || tx.Amount.Value != "123.45" || tx.Amount.Ccy != "BRL" ||
		tx.Debtor.Name != "Ana Souza" || tx.Debtor.ID.TaxID() != "52998224725" || tx.DebtorAgent.ISPB() != "99000001" ||
		tx.DebtorAcct.Number() != "00123456" || tx.DebtorAcct.Branch() != "0001" || tx.DebtorAcct.Type() != "CACC" ||
		tx.CreditorAgent.ISPB() != "99000002" || tx.CreditorAcct.Key() != "39053344705" || tx.Mandate.Tp.LclInstrm.Prtry != "DICT" ||
		tx.Purpose.Cd != "IPAY" || tx.ChrgBr != "SLEV" {
		t.Fatalf("transaction not preserved: %+v", tx)
	}
	hdr, err := iso.Header(body)
	if err != nil || hdr.MsgDefIdr != iso.Pacs008ID || hdr.Fr.ISPB() != "00038166" || hdr.To.ISPB() != "99000002" {
		t.Fatalf("header = %+v, %v", hdr, err)
	}
}

func TestPacs002RoundTrip(t *testing.T) {
	st := iso.NewTxStatus("E9900000120261114120000000000001", iso.StatusRejected).WithReason("AC03", "no such account")
	body, err := iso.NewPacs002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:00.000Z", st).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := iso.ParsePacs002(body)
	if err != nil {
		t.Fatal(err)
	}
	sts := got.Statuses()
	if len(sts) != 1 || sts[0].Status != "RJCT" || sts[0].ReasonCode() != "AC03" || sts[0].OrgnlEndToEnd != "E9900000120261114120000000000001" {
		t.Fatalf("statuses = %+v", sts)
	}
}

func buildPacs004() *iso.Pacs004 {
	r := iso.NewReturn("D9900000220261114120000000000001", "E9900000120261114120000000000001", "10.50", "MD06", "99000002", "99000001")
	r.OrgnlTxRef.RmtInf = &struct {
		Ustrd string `xml:"Ustrd"`
	}{Ustrd: "refund"}
	return iso.NewPacs004("99000002", "00038166", "M99000002abcdefghijklmnopqrstuvw", "2026-11-14T12:00:00.000Z", r)
}

func TestPacs004RoundTrip(t *testing.T) {
	body, err := buildPacs004().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := iso.ParsePacs004(body)
	if err != nil || len(got.Returns()) != 1 {
		t.Fatalf("parse: %v", err)
	}
	r := got.Returns()[0]
	if r.RtrID != "D9900000220261114120000000000001" || r.OrgnlEndToEnd != "E9900000120261114120000000000001" || r.Amount.Value != "10.50" ||
		r.Reason() != "MD06" || r.Info() != "refund" || r.OrgnlTxRef.DbtrAgt.ISPB() != "99000002" || r.OrgnlTxRef.CdtrAgt.ISPB() != "99000001" ||
		r.Priority != "HIGH" || r.ChrgBr != "SLEV" || got.Group().SttlmInf.Method != "CLRG" {
		t.Fatalf("return not preserved: %+v", r)
	}
}

func buildTrck002() *iso.Trck002 {
	tx := iso.NewTrackedTx("E9900000120261114120000000000001", "25.90", "2026-11-14T11:55:00.000Z")
	tx.SetDebtor("52998224725", "99000001", "0001", "00123456", "CACC")
	tx.SetCreditor("11144477735", "99000001", "0001", "00234567", "SVGS", "11144477735")
	return iso.NewTrck002("99000001", "00038166", "M99000001abcdefghijklmnopqrstuvw", "2026-11-14T12:00:00.000Z", tx)
}

func TestTrck002RoundTrip(t *testing.T) {
	body, err := buildTrck002().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := iso.ParseTrck002(body)
	if err != nil || len(got.Transactions()) != 1 {
		t.Fatalf("parse: %v", err)
	}
	tx := got.Transactions()[0]
	if got.Status() != "ACCC" || got.MsgID() != "M99000001abcdefghijklmnopqrstuvw" || tx.ID() != "E9900000120261114120000000000001" ||
		tx.Initiation() != "DICT" || tx.PmtScnro.Prtry != "BOK1" || tx.Amount.Value != "25.90" || tx.SettledAt() != "2026-11-14T11:55:00.000Z" ||
		tx.Debtor.TaxID() != "52998224725" || tx.Creditor.TaxID() != "11144477735" || tx.DebtorAgent.ISPB() != "99000001" ||
		tx.CreditorAcct.Key() != "11144477735" || tx.DebtorAcct.Number() != "00123456" || tx.CreditorAcct.Type() != "SVGS" {
		t.Fatalf("transaction not preserved: %+v", tx)
	}
	tx.PmtID.InstrID = "D9900000120261114120000000000001"
	if tx.ID() != "D9900000120261114120000000000001" {
		t.Fatalf("ID() = %q; a return is identified by its InstrId", tx.ID())
	}
}

func TestCamt025RoundTrip(t *testing.T) {
	rs := []iso.Receipt{
		iso.NewReceipt("M99000001abcdefghijklmnopqrstuvw", "E9900000120261114120000000000001", "", ""),
		iso.NewReceipt("M99000001abcdefghijklmnopqrstuvw", "D9900000120261114120000000000002", "AG01", "external transfer"),
	}
	body, err := iso.NewCamt025("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:01.000Z", rs).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := iso.ParseCamt025(body)
	if err != nil || len(got.Receipts()) != 2 {
		t.Fatalf("parse: %v", err)
	}
	ok, rj := got.Receipts()[0], got.Receipts()[1]
	if ok.Status() != "ACPT" || ok.ReasonCode() != "" || ok.Ref() != "E9900000120261114120000000000001" || ok.OrgnlMsgID.MsgID != "M99000001abcdefghijklmnopqrstuvw" {
		t.Fatalf("accepted receipt = %+v", ok)
	}
	if rj.Status() != "RJCT" || rj.ReasonCode() != "AG01" || rj.Ref() != "D9900000120261114120000000000002" {
		t.Fatalf("rejected receipt = %+v", rj)
	}
}

func TestForReturn(t *testing.T) {
	st := iso.NewTxStatus("E9900000120261114120000000000001", iso.StatusSettledPayer).ForReturn("D9900000220261114120000000000001")
	if st.OrgnlInstrID != "D9900000220261114120000000000001" || st.OrgnlEndToEnd != "E9900000120261114120000000000001" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAdmi002RoundTrip(t *testing.T) {
	body, err := iso.NewAdmi002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:00.000Z",
		"UEkBbgR78VqKK/uvuq9O0r9bqXyBH9Ur", "XML inválido", "Document", "could not read the XML").Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := iso.ParseAdmi002(body)
	if err != nil || got.Ref() != "UEkBbgR78VqKK/uvuq9O0r9bqXyBH9Ur" || got.Reason() != "XML inválido" || got.AppHdr.MsgDefIdr != iso.Admi002ID {
		t.Fatalf("parsed = %+v, %v", got, err)
	}
}

func TestAdmi002Clip(t *testing.T) {
	long := strings.Repeat("x", 500)
	m := iso.NewAdmi002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:00.000Z", long, long, long, long)
	if len(m.Ref()) != 33 || len([]rune(m.Reason())) != 35 || len(m.Document.Admi.Rsn.Description) != 350 || len(m.Document.Admi.Rsn.ErrorLocation) != 350 {
		t.Fatalf("lengths: ref %d, reason %d, description %d, location %d", len(m.Ref()), len([]rune(m.Reason())), len(m.Document.Admi.Rsn.Description), len(m.Document.Admi.Rsn.ErrorLocation))
	}
}

func TestAmountAndTime(t *testing.T) {
	ts, err := iso.ParseTime("2026-11-14T12:00:00.000Z")
	if err != nil || iso.FormatTime(ts) != "2026-11-14T12:00:00.000Z" {
		t.Fatalf("time round trip: %v, %v", ts, err)
	}
	if _, err := iso.ParseTime("2026-11-14 12:00:00"); err == nil {
		t.Fatal("a time in another format must be refused")
	}
}

// The official samples must parse, and a message we write must read back the same way.
func TestOfficialSamples(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(pkg, "exemplos", "pacs008", "*.xml"))
	if len(files) == 0 {
		t.Skip("catalog samples not found")
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m, err := iso.ParsePacs008(body)
		if err != nil || len(m.Transactions()) == 0 || m.Transactions()[0].PmtID.EndToEnd == "" {
			t.Errorf("%s: %v", filepath.Base(f), err)
			continue
		}
		out, err := m.Marshal()
		if err != nil {
			t.Errorf("%s: marshal: %v", filepath.Base(f), err)
			continue
		}
		again, err := iso.ParsePacs008(out)
		if err != nil || !reflect.DeepEqual(m.Transactions(), again.Transactions()) {
			t.Errorf("%s: transactions changed after marshal and parse (err %v)", filepath.Base(f), err)
		}
	}
	files, _ = filepath.Glob(filepath.Join(pkg, "exemplos", "trck002", "*.xml"))
	for _, f := range files {
		body, _ := os.ReadFile(f)
		m, err := iso.ParseTrck002(body)
		if err != nil || len(m.Transactions()) == 0 || m.Transactions()[0].ID() == "" || m.Status() != "ACCC" {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
	files, _ = filepath.Glob(filepath.Join(pkg, "exemplos", "camt025", "*.xml"))
	for _, f := range files {
		body, _ := os.ReadFile(f)
		m, err := iso.ParseCamt025(body)
		if err != nil || len(m.Receipts()) == 0 || m.Receipts()[0].Status() == "" || m.Receipts()[0].Ref() == "" {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
	files, _ = filepath.Glob(filepath.Join(pkg, "exemplos", "pacs004", "*.xml"))
	for _, f := range files {
		body, _ := os.ReadFile(f)
		m, err := iso.ParsePacs004(body)
		if err != nil || len(m.Returns()) == 0 || m.Returns()[0].RtrID == "" || m.Returns()[0].Reason() == "" {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
	files, _ = filepath.Glob(filepath.Join(pkg, "exemplos", "pacs002", "*.xml"))
	for _, f := range files {
		body, _ := os.ReadFile(f)
		m, err := iso.ParsePacs002(body)
		if err != nil || len(m.Statuses()) == 0 || m.Statuses()[0].Status == "" {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
}

// What the mock writes must be valid against the official XSD. The empty <Sgntr/> is left
// out of the check: the schema requires an XML signature there, and the mock has none.
func TestXSD(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not installed")
	}
	p008, _ := buildPacs008().Marshal()
	settled := iso.NewTxStatus("E9900000120261114120000000000001", iso.StatusSettledPayer).
		WithSettlement("2026-11-14T12:00:05.000Z", "2026-11-14")
	p002ok, _ := iso.NewPacs002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:05.000Z", settled).Marshal()
	rejected := iso.NewTxStatus("E9900000120261114120000000000001", iso.StatusRejected).WithReason("AC03", "no such account")
	p002rj, _ := iso.NewPacs002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:05.000Z", rejected).Marshal()

	p004, _ := buildPacs004().Marshal()
	trck, _ := buildTrck002().Marshal()
	camtOK, _ := iso.NewCamt025("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:01.000Z",
		[]iso.Receipt{iso.NewReceipt("M99000001abcdefghijklmnopqrstuvw", "E9900000120261114120000000000001", "", "")}).Marshal()
	camtRJ, _ := iso.NewCamt025("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:01.000Z",
		[]iso.Receipt{iso.NewReceipt("M99000001abcdefghijklmnopqrstuvw", "E9900000120261114120000000000001", "FF08", "too old")}).Marshal()
	admi, _ := iso.NewAdmi002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:05.000Z",
		"UEkBbgR78VqKK/uvuq9O0r9bqXyBH9Ur", "XML inválido", "Document", "could not read the XML").Marshal()

	for name, c := range map[string]struct {
		xsd  string
		body []byte
	}{
		"admi.002":          {"admi.002.spi.1.5.xsd", admi},
		"pacs.004":          {"pacs.004.spi.1.5.xsd", p004},
		"trck.002":          {"trck.002.spi.1.1.xsd", trck},
		"camt.025 accepted": {"camt.025.spi.1.0.xsd", camtOK},
		"camt.025 rejected": {"camt.025.spi.1.0.xsd", camtRJ},
		"pacs.008":          {"pacs.008.spi.1.16.xsd", p008},
		"pacs.002 settled":  {"pacs.002.spi.1.18.xsd", p002ok},
		"pacs.002 rejected": {"pacs.002.spi.1.18.xsd", p002rj},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "msg.xml")
			if err := os.WriteFile(file, c.body, 0o600); err != nil {
				t.Fatal(err)
			}
			out, _ := exec.Command("xmllint", "--noout", "--schema", filepath.Join(pkg, "xsd", c.xsd), file).CombinedOutput()
			var problems []string
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "Schemas validity error") && !strings.Contains(line, "Sgntr") {
					problems = append(problems, line)
				}
			}
			if len(problems) > 0 {
				t.Fatalf("schema errors:\n%s\n\nmessage:\n%s", strings.Join(problems, "\n"), c.body)
			}
		})
	}
}
