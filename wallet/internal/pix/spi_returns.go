package pix

import (
	"encoding/xml"
	"fmt"
	"slices"
	"time"
)

const (
	msgPacs004 = "pacs.004.spi.1.5"
	msgTrck002 = "trck.002.spi.1.1"

	nsPacs004 = "https://www.bcb.gov.br/pi/pacs.004/1.5"
	nsTrck002 = "https://www.bcb.gov.br/pi/trck.002/1.1"

	maxReportedTxs = 500
	// maxReportAge is how long after settling a transaction can still be reported (art. 2-A of IN BCB 32/2020).
	maxReportAge = 30 * 24 * time.Hour
)

// MD06: the only reason a return on the wallet's own initiative needs.
const ReturnReasonCustomer = "MD06"

const (
	ReceiptAccepted = "ACPT"
	ReceiptRejected = "RJCT"
)

const (
	ScenarioSameParticipant = "BOK1" // both customers belong to the same participant
	ScenarioSameSettler     = "BOK2" // different participants that settle through the same one
)

// From is the participant that returns the money (it holds the debited account), To the payer's.
type PaymentReturn struct {
	MsgID       string // set when read
	ReturnID    string // the RtrId, see NewReturnID
	EndToEndID  string // of the original payment
	AmountCents int64
	ReasonCode  string // ReturnReasonCustomer when empty
	Detail      string // why, up to 105 characters
	Message     string // free text between users, up to 140 characters
	From        string // ISPB; set when read, the sender is the ispb passed to NewPacs004
	To          string // ISPB
}

// The SPI answers with a pacs.002 whose OrgnlInstrId is the ReturnID.
func NewPacs004(ispb string, r PaymentReturn, now time.Time) (msgID string, body []byte, err error) {
	switch {
	case r.AmountCents <= 0:
		return "", nil, fmt.Errorf("%w: amount must be positive", ErrBadRequest)
	case !returnIDRe.MatchString(r.ReturnID):
		return "", nil, fmt.Errorf("%w: RtrId %q is malformed (see NewReturnID)", ErrBadRequest, r.ReturnID)
	case !endToEndRe.MatchString(r.EndToEndID):
		return "", nil, fmt.Errorf("%w: EndToEndId %q is malformed", ErrBadRequest, r.EndToEndID)
	case len(r.To) != 8:
		return "", nil, fmt.Errorf("%w: the ISPB of the receiving participant must have 8 digits, got %q", ErrBadRequest, r.To)
	case r.To == ispb:
		// the SPI rejects it with AG12; a return inside the institution is a ledger movement, not a message
		return "", nil, fmt.Errorf("%w: a return to the same participant does not go through the SPI", ErrBadRequest)
	}
	if msgID, err = NewMessageID(ispb); err != nil {
		return "", nil, err
	}
	created := now.UTC().Format(timeLayout)

	tx := returnXML{
		RtrID:         r.ReturnID,
		OrgnlEndToEnd: r.EndToEndID,
		Amount:        amountXML{Currency: "BRL", Value: formatAmount(r.AmountCents)},
		Priority:      "HIGH",
		ChrgBr:        "SLEV",
	}
	tx.RtrRsnInf.Rsn.Cd = ReturnReasonCustomer
	if r.ReasonCode != "" {
		tx.RtrRsnInf.Rsn.Cd = r.ReasonCode
	}
	tx.RtrRsnInf.AddtlInf = clip(r.Detail, 105)
	if r.Message != "" {
		tx.OrgnlTxRef.RmtInf = &remittanceXML{Text: clip(r.Message, 140)}
	}
	tx.OrgnlTxRef.DbtrAgt, tx.OrgnlTxRef.CdtrAgt = agentOf(ispb), agentOf(r.To)

	m := pacs004XML{Xmlns: nsPacs004}
	m.AppHdr = newAppHdr(ispb, spiISPB, msgID, msgPacs004, created)
	g := &m.Document.PmtRtr.GrpHdr
	g.MsgID, g.CreDtTm, g.NbOfTxs = msgID, created, "1"
	g.SttlmInf.Method = "CLRG"
	m.Document.PmtRtr.TxInf = []returnXML{tx}

	body, err = marshalMessage(m)
	return msgID, body, err
}

// The wallet answers each return with a pacs.002 whose InstrID is the ReturnID.
func ParsePacs004(body []byte) ([]PaymentReturn, error) {
	var m pacs004XML
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("spi: invalid pacs.004: %w", err)
	}
	out := make([]PaymentReturn, 0, len(m.Document.PmtRtr.TxInf))
	for _, tx := range m.Document.PmtRtr.TxInf {
		cents, err := parseAmount(tx.Amount.Value)
		if err != nil {
			return nil, err
		}
		r := PaymentReturn{
			MsgID:       m.Document.PmtRtr.GrpHdr.MsgID,
			ReturnID:    tx.RtrID,
			EndToEndID:  tx.OrgnlEndToEnd,
			AmountCents: cents,
			ReasonCode:  tx.RtrRsnInf.Rsn.Cd,
			Detail:      tx.RtrRsnInf.AddtlInf,
			From:        tx.OrgnlTxRef.DbtrAgt.FinInstnId.ClrSysMmbId.MmbID,
			To:          tx.OrgnlTxRef.CdtrAgt.FinInstnId.ClrSysMmbId.MmbID,
		}
		if tx.OrgnlTxRef.RmtInf != nil {
			r.Message = tx.OrgnlTxRef.RmtInf.Text
		}
		out = append(out, r)
	}
	return out, nil
}

var (
	// initiationsWithKey are the ones that carry the Pix key; MANU and AUTO must not.
	initiationsWithKey = []string{"APDN", "APES", "DICT", "INIC", "QRDN", "QRES"}
	initiationsNoKey   = []string{"MANU", "AUTO"}
)

type ReportedTx struct {
	EndToEndID  string // of the payment; for a return, of the original payment
	ReturnID    string // the RtrId, only when the transaction is a return (the initiation is then MANU)
	AmountCents int64
	SettledAt   time.Time // when it settled; at most 30 days ago, never in the future
	Payer       Party     // TaxID, ISPB, Branch, Account, AccountType
	Receiver    Party     // the account cannot be a salary account
	Key         string    // the Pix key the payer used, empty for a payment typed by account number
	// Initiation is how the payment was started. Empty means DICT when there is a Key and MANU when there is not.
	Initiation string
	// Scenario is ScenarioSameParticipant or ScenarioSameSettler. Empty means the first when both parties have the
	// same ISPB. ScenarioSameSettler is not inferred: the SPI rejects a report of participants that settle through
	// different ones with AG01.
	Scenario string
}

// The SPI answers with a camt.025 whose receipts name that MsgId.
func NewTrck002(ispb string, txs []ReportedTx, now time.Time) (msgID string, body []byte, err error) {
	if len(txs) < 1 || len(txs) > maxReportedTxs {
		return "", nil, fmt.Errorf("%w: a report takes 1 to %d transactions, got %d", ErrBadRequest, maxReportedTxs, len(txs))
	}
	wire := make([]reportedTxXML, 0, len(txs))
	for i, t := range txs {
		x, err := t.wire(now)
		if err != nil {
			return "", nil, fmt.Errorf("transaction %d: %w", i, err)
		}
		wire = append(wire, x)
	}
	if msgID, err = NewMessageID(ispb); err != nil {
		return "", nil, err
	}
	created := now.UTC().Format(timeLayout)

	m := trck002XML{Xmlns: nsTrck002}
	m.AppHdr = newAppHdr(ispb, spiISPB, msgID, msgTrck002, created)
	r := &m.Document.PmtStsTrckrRpt
	r.GrpHdr.MsgID, r.GrpHdr.CreDtTm = msgID, created
	r.TrckrStsAndTx.TxSts.Sts = StatusSettledOutside
	r.TrckrStsAndTx.Tx = wire

	body, err = marshalMessage(m)
	return msgID, body, err
}

const StatusSettledOutside = "ACCC"

func (t ReportedTx) wire(now time.Time) (reportedTxXML, error) {
	initiation := t.Initiation
	if initiation == "" {
		initiation = "MANU"
		if t.Key != "" {
			initiation = "DICT"
		}
	}
	switch {
	case t.AmountCents <= 0:
		return reportedTxXML{}, fmt.Errorf("%w: amount must be positive", ErrBadRequest)
	case !endToEndRe.MatchString(t.EndToEndID):
		return reportedTxXML{}, fmt.Errorf("%w: EndToEndId %q is malformed", ErrBadRequest, t.EndToEndID)
	case t.ReturnID != "" && !returnIDRe.MatchString(t.ReturnID):
		return reportedTxXML{}, fmt.Errorf("%w: RtrId %q is malformed", ErrBadRequest, t.ReturnID)
	case t.ReturnID != "" && initiation != "MANU":
		return reportedTxXML{}, fmt.Errorf("%w: a return is initiated as MANU, not %s", ErrBadRequest, initiation)
	case !slices.Contains(initiationsWithKey, initiation) && !slices.Contains(initiationsNoKey, initiation):
		return reportedTxXML{}, fmt.Errorf("%w: unknown initiation %q", ErrBadRequest, initiation)
	case slices.Contains(initiationsNoKey, initiation) && t.Key != "":
		return reportedTxXML{}, fmt.Errorf("%w: a %s payment carries no Pix key", ErrBadRequest, initiation)
	case slices.Contains(initiationsWithKey, initiation) && t.Key == "":
		return reportedTxXML{}, fmt.Errorf("%w: a %s payment needs its Pix key", ErrBadRequest, initiation)
	case t.Receiver.AccountType == AccountSalary:
		return reportedTxXML{}, fmt.Errorf("%w: the receiving account cannot be a salary account", ErrBadRequest)
	case t.SettledAt.IsZero() || t.SettledAt.After(now):
		return reportedTxXML{}, fmt.Errorf("%w: the settlement time is missing or in the future", ErrBadRequest)
	case now.Sub(t.SettledAt) > maxReportAge:
		return reportedTxXML{}, fmt.Errorf("%w: settled more than 30 days ago, too late to report", ErrBadRequest)
	case t.Payer.TaxID == "" || t.Receiver.TaxID == "" || t.Payer.ISPB == "" || t.Receiver.ISPB == "":
		return reportedTxXML{}, fmt.Errorf("%w: both parties need a tax ID and an ISPB", ErrBadRequest)
	}
	scenario := t.Scenario
	if scenario == "" {
		if t.Payer.ISPB != t.Receiver.ISPB {
			return reportedTxXML{}, fmt.Errorf("%w: the parties belong to different participants; say the scenario", ErrBadRequest)
		}
		scenario = ScenarioSameParticipant
	}

	var x reportedTxXML
	x.PmtID.InstrID, x.PmtID.EndToEnd = t.ReturnID, t.EndToEndID
	x.PmtTpInf.LclInstrm.Prtry = initiation
	x.PmtScnro.Prtry = scenario
	x.Amount = amountXML{Currency: "BRL", Value: formatAmount(t.AmountCents)}
	x.ReqdExctnDt.DtTm = t.SettledAt.UTC().Format(timeLayout)
	x.Debtor, x.DebtorAcct, x.DebtorAgt = partyID(t.Payer.TaxID), bankAccount(t.Payer, ""), agentOf(t.Payer.ISPB)
	x.Creditor, x.CreditorAcct, x.CreditorAgt = partyID(t.Receiver.TaxID), bankAccount(t.Receiver, t.Key), agentOf(t.Receiver.ISPB)
	return x, nil
}

type Receipt struct {
	ReportMsgID string // the MsgId of the trck.002
	Ref         string // the RtrId of the transaction when it is a return, otherwise its EndToEndId
	Status      string // ReceiptAccepted or ReceiptRejected
	ReasonCode  string // with ReceiptRejected: AG01, AM01, AM18, DT02, ED05, FF08, RC09 or RC10
	Detail      string
}

func (r Receipt) Rejected() bool { return r.Status == ReceiptRejected }

func ParseCamt025(body []byte) ([]Receipt, error) {
	var m camt025XML
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("spi: invalid camt.025: %w", err)
	}
	var out []Receipt
	for _, d := range m.Document.Rct.RctDtls {
		r := Receipt{ReportMsgID: d.OrgnlMsgID.MsgID, Ref: d.OrgnlPmtID.PrtryID, Status: d.ReqHdlg.Sts.Cd}
		if d.ReqHdlg.StsRsn != nil {
			r.ReasonCode, r.Detail = d.ReqHdlg.StsRsn.Rsn.Prtry, d.ReqHdlg.StsRsn.AddtlInf
		}
		out = append(out, r)
	}
	return out, nil
}
