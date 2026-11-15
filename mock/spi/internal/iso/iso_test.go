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

func TestFamily(t *testing.T) {
	for in, want := range map[string]string{"pacs.008.spi.1.16": "pacs.008", "admi.002.spi.1.5": "admi.002", "x": "x"} {
		if got := iso.Family(in); got != want {
			t.Errorf("Family(%q) = %q, want %q", in, got, want)
		}
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
func TestGeneratedMessagesValidateAgainstXSD(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not installed")
	}
	p008, _ := buildPacs008().Marshal()
	settled := iso.NewTxStatus("E9900000120261114120000000000001", iso.StatusSettledPayer).
		WithSettlement("2026-11-14T12:00:05.000Z", "2026-11-14")
	p002ok, _ := iso.NewPacs002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:05.000Z", settled).Marshal()
	rejected := iso.NewTxStatus("E9900000120261114120000000000001", iso.StatusRejected).WithReason("AC03", "no such account")
	p002rj, _ := iso.NewPacs002("00038166", "99000001", "M00038166abcdefghijklmnopqrstuvw", "2026-11-14T12:00:05.000Z", rejected).Marshal()

	for name, c := range map[string]struct {
		xsd  string
		body []byte
	}{
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
