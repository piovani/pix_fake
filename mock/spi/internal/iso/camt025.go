package iso

import (
	"encoding/xml"
	"fmt"
)

// Camt025ID and the namespace of the receipt the SPI sends for a trck.002.
const (
	Camt025ID = "camt.025.spi.1.0"
	nsCamt025 = "https://www.bcb.gov.br/pi/camt.025/1.0"

	// Receipt statuses.
	ReceiptAccepted = "ACPT"
	ReceiptRejected = "RJCT"
)

// Camt025 tells a participant whether each transaction of its trck.002 was recorded.
type Camt025 struct {
	XMLName  xml.Name `xml:"Envelope"`
	Xmlns    string   `xml:"xmlns,attr"`
	AppHdr   AppHdr   `xml:"AppHdr"`
	Document struct {
		Rct struct {
			MsgHdr struct {
				MsgID   string `xml:"MsgId"`
				CreDtTm string `xml:"CreDtTm"`
			} `xml:"MsgHdr"`
			RctDtls []Receipt `xml:"RctDtls"`
		} `xml:"Rct"`
	} `xml:"Document"`
}

// Receipt is the outcome of one reported transaction.
type Receipt struct {
	OrgnlMsgID struct {
		MsgID string `xml:"MsgId"`
	} `xml:"OrgnlMsgId"`
	// OrgnlPmtID is the RtrId or the EndToEndId, as reported.
	OrgnlPmtID struct {
		PrtryID string `xml:"PrtryId"`
	} `xml:"OrgnlPmtId"`
	ReqHdlg struct {
		Sts struct {
			Cd string `xml:"Cd"`
		} `xml:"Sts"`
		StsRsn *struct {
			Rsn struct {
				Prtry string `xml:"Prtry"`
			} `xml:"Rsn"`
			AddtlInf string `xml:"AddtlInf,omitempty"`
		} `xml:"StsRsn,omitempty"`
	} `xml:"ReqHdlg"`
}

// Status returns ACPT or RJCT.
func (r Receipt) Status() string { return r.ReqHdlg.Sts.Cd }

// ReasonCode returns the rejection reason, empty when accepted.
func (r Receipt) ReasonCode() string {
	if r.ReqHdlg.StsRsn == nil {
		return ""
	}
	return r.ReqHdlg.StsRsn.Rsn.Prtry
}

// Ref returns the identification of the reported transaction the receipt answers.
func (r Receipt) Ref() string { return r.OrgnlPmtID.PrtryID }

// NewReceipt builds the outcome of one transaction of the message originalMsgID. reason is empty when accepted.
func NewReceipt(originalMsgID, ref, reason, detail string) Receipt {
	var r Receipt
	r.OrgnlMsgID.MsgID, r.OrgnlPmtID.PrtryID = originalMsgID, ref
	r.ReqHdlg.Sts.Cd = ReceiptAccepted
	if reason != "" {
		r.ReqHdlg.Sts.Cd = ReceiptRejected
		r.ReqHdlg.StsRsn = &struct {
			Rsn struct {
				Prtry string `xml:"Prtry"`
			} `xml:"Rsn"`
			AddtlInf string `xml:"AddtlInf,omitempty"`
		}{}
		r.ReqHdlg.StsRsn.Rsn.Prtry = reason
		r.ReqHdlg.StsRsn.AddtlInf = clip(detail, 105)
	}
	return r
}

// NewCamt025 builds the receipt message from the SPI to a participant.
func NewCamt025(from, to, msgID, createdAt string, receipts []Receipt) *Camt025 {
	m := &Camt025{Xmlns: nsCamt025}
	m.AppHdr = AppHdr{Fr: NewParty(from), To: NewParty(to), BizMsgIdr: msgID, MsgDefIdr: Camt025ID, CreDt: createdAt}
	m.Document.Rct.MsgHdr.MsgID, m.Document.Rct.MsgHdr.CreDtTm = msgID, createdAt
	m.Document.Rct.RctDtls = receipts
	return m
}

// ParseCamt025 reads a camt.025 message.
func ParseCamt025(body []byte) (*Camt025, error) {
	var m Camt025
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid camt.025: %w", err)
	}
	return &m, nil
}

// Receipts returns the outcomes in the message.
func (m *Camt025) Receipts() []Receipt { return m.Document.Rct.RctDtls }

// Marshal returns the message as XML with the declaration.
func (m *Camt025) Marshal() ([]byte, error) { return marshal(m) }
