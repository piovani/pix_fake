package iso

import (
	"encoding/xml"
	"fmt"
)

// Trck002ID and the namespace of the report of transactions settled outside the SPI.
const (
	Trck002ID = "trck.002.spi.1.1"
	nsTrck002 = "https://www.bcb.gov.br/pi/trck.002/1.1"

	// StatusTracked is the only status a trck.002 carries: the reported transaction settled.
	StatusTracked = "ACCC"
)

// Trck002 is a participant's report of Pix transactions it settled in its own systems, such as a
// payment between two customers of the same institution (a book transfer).
type Trck002 struct {
	XMLName  xml.Name `xml:"Envelope"`
	Xmlns    string   `xml:"xmlns,attr"`
	AppHdr   AppHdr   `xml:"AppHdr"`
	Document struct {
		PmtStsTrckrRpt struct {
			GrpHdr struct {
				MsgID   string `xml:"MsgId"`
				CreDtTm string `xml:"CreDtTm"`
			} `xml:"GrpHdr"`
			TrckrStsAndTx struct {
				TxSts struct {
					Sts string `xml:"Sts"`
				} `xml:"TxSts"`
				Tx []TrackedTx `xml:"Tx"`
			} `xml:"TrckrStsAndTx"`
		} `xml:"PmtStsTrckrRpt"`
	} `xml:"Document"`
}

// TrackedTx is one reported transaction.
type TrackedTx struct {
	PmtID struct {
		// InstrID is the RtrId when the transaction is a return; EndToEnd is then that of the original payment.
		InstrID  string `xml:"InstrId,omitempty"`
		EndToEnd string `xml:"EndToEndId"`
	} `xml:"PmtId"`
	PmtTpInf struct {
		LclInstrm struct {
			Prtry string `xml:"Prtry"`
		} `xml:"LclInstrm"`
	} `xml:"PmtTpInf"`
	// Scenario is BOK1 (same participant on both sides) or BOK2 (different participants that settle through the same one).
	PmtScnro struct {
		Prtry string `xml:"Prtry"`
	} `xml:"PmtScnro"`
	Amount      Amount `xml:"IntrBkSttlmAmt"`
	ReqdExctnDt struct {
		DtTm string `xml:"DtTm"`
	} `xml:"ReqdExctnDt"`
	Debtor        PtyID   `xml:"Dbtr"`
	DebtorAcct    Account `xml:"DbtrAcct"`
	DebtorAgent   Agent   `xml:"DbtrAgt"`
	CreditorAgent Agent   `xml:"CdtrAgt"`
	Creditor      PtyID   `xml:"Cdtr"`
	CreditorAcct  Account `xml:"CdtrAcct"`
}

// PtyID is the CPF or CNPJ of a party (Pty/Id/PrvtId/Othr/Id).
type PtyID struct {
	Pty struct {
		ID PrvtID `xml:"Id"`
	} `xml:"Pty"`
}

// TaxID returns the CPF or CNPJ.
func (p PtyID) TaxID() string { return p.Pty.ID.TaxID() }

// ID returns what identifies the transaction: the RtrId of a return, otherwise the EndToEndId.
func (t TrackedTx) ID() string {
	if t.PmtID.InstrID != "" {
		return t.PmtID.InstrID
	}
	return t.PmtID.EndToEnd
}

// Initiation returns how the payment was initiated (MANU, DICT...).
func (t TrackedTx) Initiation() string { return t.PmtTpInf.LclInstrm.Prtry }

// SettledAt returns the date and time the transaction settled, as written in the message.
func (t TrackedTx) SettledAt() string { return t.ReqdExctnDt.DtTm }

// ParseTrck002 reads a trck.002 message.
func ParseTrck002(body []byte) (*Trck002, error) {
	var m Trck002
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid trck.002: %w", err)
	}
	return &m, nil
}

// Transactions returns the reported transactions.
func (m *Trck002) Transactions() []TrackedTx { return m.Document.PmtStsTrckrRpt.TrckrStsAndTx.Tx }

// Status returns the status declared for the transactions.
func (m *Trck002) Status() string { return m.Document.PmtStsTrckrRpt.TrckrStsAndTx.TxSts.Sts }

// MsgID returns the message identification of the group header.
func (m *Trck002) MsgID() string { return m.Document.PmtStsTrckrRpt.GrpHdr.MsgID }

// CreatedAt returns the creation date and time of the group header.
func (m *Trck002) CreatedAt() string { return m.Document.PmtStsTrckrRpt.GrpHdr.CreDtTm }

// NewTrck002 builds a report from a participant to the SPI.
func NewTrck002(from, to, msgID, createdAt string, txs ...TrackedTx) *Trck002 {
	m := &Trck002{Xmlns: nsTrck002}
	m.AppHdr = AppHdr{Fr: NewParty(from), To: NewParty(to), BizMsgIdr: msgID, MsgDefIdr: Trck002ID, CreDt: createdAt}
	r := &m.Document.PmtStsTrckrRpt
	r.GrpHdr.MsgID, r.GrpHdr.CreDtTm = msgID, createdAt
	r.TrckrStsAndTx.TxSts.Sts = StatusTracked
	r.TrckrStsAndTx.Tx = txs
	return m
}

// NewTrackedTx starts a reported transaction between two customers of the same participant (scenario BOK1),
// initiated manually. Fill the parties with SetDebtor and SetCreditor.
func NewTrackedTx(e2e, amount, settledAt string) TrackedTx {
	var t TrackedTx
	t.PmtID.EndToEnd = e2e
	t.PmtTpInf.LclInstrm.Prtry = "MANU"
	t.PmtScnro.Prtry = "BOK1"
	t.Amount = Amount{Ccy: "BRL", Value: amount}
	t.ReqdExctnDt.DtTm = settledAt
	return t
}

// SetDebtor fills the payer side.
func (t *TrackedTx) SetDebtor(taxID, ispb, branch, number, accType string) {
	t.Debtor.Pty.ID = newPrvtID(taxID)
	t.DebtorAgent = newAgent(ispb)
	t.DebtorAcct.ID.Othr.Number, t.DebtorAcct.ID.Othr.Branch, t.DebtorAcct.Tp.Cd = number, branch, accType
}

// SetCreditor fills the receiver side. A key makes the initiation DICT; without one it stays MANU.
func (t *TrackedTx) SetCreditor(taxID, ispb, branch, number, accType, key string) {
	t.Creditor.Pty.ID = newPrvtID(taxID)
	t.CreditorAgent = newAgent(ispb)
	t.CreditorAcct.ID.Othr.Number, t.CreditorAcct.ID.Othr.Branch, t.CreditorAcct.Tp.Cd = number, branch, accType
	if key != "" {
		t.CreditorAcct.Prxy = &Proxy{ID: key}
		t.PmtTpInf.LclInstrm.Prtry = "DICT"
	}
}

// Marshal returns the message as XML with the declaration.
func (m *Trck002) Marshal() ([]byte, error) { return marshal(m) }
