package iso

import (
	"encoding/xml"
	"fmt"
)

// Transaction status codes of a pacs.002.
const (
	StatusAcceptedByReceiver = "ACSP" // the receiving participant accepts the order
	StatusSettledPayer       = "ACSC" // the SPI tells the payer's participant that it settled
	StatusSettledReceiver    = "ACCC" // the SPI tells the receiver's participant that it settled
	StatusRejected           = "RJCT"
)

// Pacs002 is a payment status report.
type Pacs002 struct {
	XMLName  xml.Name `xml:"Envelope"`
	Xmlns    string   `xml:"xmlns,attr"`
	AppHdr   AppHdr   `xml:"AppHdr"`
	Document struct {
		FIToFIPmtStsRpt struct {
			GrpHdr struct {
				MsgID   string `xml:"MsgId"`
				CreDtTm string `xml:"CreDtTm"`
			} `xml:"GrpHdr"`
			TxInfAndSts []TxStatus `xml:"TxInfAndSts"`
		} `xml:"FIToFIPmtStsRpt"`
	} `xml:"Document"`
}

// TxStatus is the status of one transaction.
type TxStatus struct {
	OrgnlInstrID  string `xml:"OrgnlInstrId"`
	OrgnlEndToEnd string `xml:"OrgnlEndToEndId"`
	Status        string `xml:"TxSts"`
	Reason        []struct {
		Rsn struct {
			Cd string `xml:"Cd"`
		} `xml:"Rsn"`
		AddtlInf []string `xml:"AddtlInf,omitempty"`
	} `xml:"StsRsnInf,omitempty"`
	SettledAt *struct {
		DtTm string `xml:"DtTm"`
	} `xml:"FctvIntrBkSttlmDt,omitempty"`
	OrgnlTxRef *struct {
		SettlementDate string `xml:"IntrBkSttlmDt"`
	} `xml:"OrgnlTxRef,omitempty"`
}

// ReasonCode returns the first reason code, empty when there is none.
func (t TxStatus) ReasonCode() string {
	if len(t.Reason) == 0 {
		return ""
	}
	return t.Reason[0].Rsn.Cd
}

// ParsePacs002 reads a pacs.002 message.
func ParsePacs002(body []byte) (*Pacs002, error) {
	var m Pacs002
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid pacs.002: %w", err)
	}
	return &m, nil
}

// Statuses returns the transaction statuses of the message.
func (m *Pacs002) Statuses() []TxStatus { return m.Document.FIToFIPmtStsRpt.TxInfAndSts }

// MsgID returns the message identification of the group header.
func (m *Pacs002) MsgID() string { return m.Document.FIToFIPmtStsRpt.GrpHdr.MsgID }

// NewPacs002 builds a status report with one transaction status.
func NewPacs002(from, to, msgID, createdAt string, st TxStatus) *Pacs002 {
	m := &Pacs002{Xmlns: nsPacs002}
	m.AppHdr = AppHdr{Fr: NewParty(from), To: NewParty(to), BizMsgIdr: msgID, MsgDefIdr: Pacs002ID, CreDt: createdAt}
	m.Document.FIToFIPmtStsRpt.GrpHdr.MsgID = msgID
	m.Document.FIToFIPmtStsRpt.GrpHdr.CreDtTm = createdAt
	m.Document.FIToFIPmtStsRpt.TxInfAndSts = []TxStatus{st}
	return m
}

// NewTxStatus builds the status of the transaction identified by its EndToEndId.
func NewTxStatus(e2e, status string) TxStatus {
	return TxStatus{OrgnlInstrID: e2e, OrgnlEndToEnd: e2e, Status: status}
}

// WithReason adds a reason code and optional detail (at most 105 characters).
func (t TxStatus) WithReason(code, detail string) TxStatus {
	if len(detail) > 105 {
		detail = detail[:105]
	}
	r := struct {
		Rsn struct {
			Cd string `xml:"Cd"`
		} `xml:"Rsn"`
		AddtlInf []string `xml:"AddtlInf,omitempty"`
	}{}
	r.Rsn.Cd = code
	if detail != "" {
		r.AddtlInf = []string{detail}
	}
	t.Reason = append(t.Reason, r)
	return t
}

// WithSettlement adds the settlement date and time and the accounting date, filled only by the SPI.
func (t TxStatus) WithSettlement(settledAt string, accountingDate string) TxStatus {
	t.SettledAt = &struct {
		DtTm string `xml:"DtTm"`
	}{DtTm: settledAt}
	t.OrgnlTxRef = &struct {
		SettlementDate string `xml:"IntrBkSttlmDt"`
	}{SettlementDate: accountingDate}
	return t
}

// Marshal returns the message as XML with the declaration.
func (m *Pacs002) Marshal() ([]byte, error) { return marshal(m) }
