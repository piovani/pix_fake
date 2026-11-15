package iso

import (
	"encoding/xml"
	"fmt"
)

// Pacs004ID is the identification of the payment return message.
const (
	Pacs004ID = "pacs.004.spi.1.5"
	nsPacs004 = "https://www.bcb.gov.br/pi/pacs.004/1.5"
)

// Pacs004 is a payment return: money going back to the payer of an earlier payment.
type Pacs004 struct {
	XMLName  xml.Name `xml:"Envelope"`
	Xmlns    string   `xml:"xmlns,attr"`
	AppHdr   AppHdr   `xml:"AppHdr"`
	Document struct {
		PmtRtr struct {
			GrpHdr ReturnGroupHeader `xml:"GrpHdr"`
			TxInf  []Return          `xml:"TxInf"`
		} `xml:"PmtRtr"`
	} `xml:"Document"`
}

// ReturnGroupHeader holds what the returns of a pacs.004 have in common.
type ReturnGroupHeader struct {
	MsgID    string `xml:"MsgId"`
	CreDtTm  string `xml:"CreDtTm"`
	NbOfTxs  string `xml:"NbOfTxs"`
	SttlmInf struct {
		Method string `xml:"SttlmMtd"`
	} `xml:"SttlmInf"`
}

// Return is one return of a pacs.004. The participant that sends it holds the account that is
// debited (DbtrAgt), the one that receives it holds the account that is credited (CdtrAgt): they are,
// respectively, the receiving and the paying participant of the original payment.
type Return struct {
	RtrID         string `xml:"RtrId"`
	OrgnlEndToEnd string `xml:"OrgnlEndToEndId"`
	Amount        Amount `xml:"RtrdIntrBkSttlmAmt"`
	Priority      string `xml:"SttlmPrty"`
	ChrgBr        string `xml:"ChrgBr"`
	RtrRsnInf     struct {
		Rsn struct {
			Cd string `xml:"Cd"`
		} `xml:"Rsn"`
		AddtlInf string `xml:"AddtlInf,omitempty"`
	} `xml:"RtrRsnInf"`
	OrgnlTxRef struct {
		RmtInf *struct {
			Ustrd string `xml:"Ustrd"`
		} `xml:"RmtInf,omitempty"`
		DbtrAgt Agent `xml:"DbtrAgt"`
		CdtrAgt Agent `xml:"CdtrAgt"`
	} `xml:"OrgnlTxRef"`
}

// Reason returns the return reason code (MD06, BE08...).
func (r Return) Reason() string { return r.RtrRsnInf.Rsn.Cd }

// Info returns the free text the user added to the return, empty when there is none.
func (r Return) Info() string {
	if r.OrgnlTxRef.RmtInf == nil {
		return ""
	}
	return r.OrgnlTxRef.RmtInf.Ustrd
}

// ParsePacs004 reads a pacs.004 message.
func ParsePacs004(body []byte) (*Pacs004, error) {
	var m Pacs004
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid pacs.004: %w", err)
	}
	return &m, nil
}

// Returns returns the returns of the message.
func (m *Pacs004) Returns() []Return { return m.Document.PmtRtr.TxInf }

// Group returns the group header.
func (m *Pacs004) Group() ReturnGroupHeader { return m.Document.PmtRtr.GrpHdr }

// NewReturn starts a return of the payment e2e. debtor is the participant that sends it (it holds the
// account that is debited) and creditor the participant that receives the money.
func NewReturn(rtrID, e2e, amount, reason, debtor, creditor string) Return {
	var r Return
	r.RtrID, r.OrgnlEndToEnd = rtrID, e2e
	r.Amount = Amount{Ccy: "BRL", Value: amount}
	r.Priority, r.ChrgBr = "HIGH", "SLEV"
	r.RtrRsnInf.Rsn.Cd = reason
	r.OrgnlTxRef.DbtrAgt, r.OrgnlTxRef.CdtrAgt = newAgent(debtor), newAgent(creditor)
	return r
}

// NewPacs004 builds a message carrying one return.
func NewPacs004(from, to, msgID, createdAt string, r Return) *Pacs004 {
	m := &Pacs004{Xmlns: nsPacs004}
	m.AppHdr = AppHdr{Fr: NewParty(from), To: NewParty(to), BizMsgIdr: msgID, MsgDefIdr: Pacs004ID, CreDt: createdAt}
	g := &m.Document.PmtRtr.GrpHdr
	g.MsgID, g.CreDtTm, g.NbOfTxs = msgID, createdAt, "1"
	g.SttlmInf.Method = "CLRG"
	m.Document.PmtRtr.TxInf = []Return{r}
	return m
}

// Marshal returns the message as XML with the declaration.
func (m *Pacs004) Marshal() ([]byte, error) { return marshal(m) }
