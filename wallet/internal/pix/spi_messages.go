package pix

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	spiISPB = "00038166" // the SPI's own ISPB, the addressee of every message

	msgPacs008 = "pacs.008.spi.1.16"
	msgPacs002 = "pacs.002.spi.1.18"

	nsPacs008 = "https://www.bcb.gov.br/pi/pacs.008/1.16"
	nsPacs002 = "https://www.bcb.gov.br/pi/pacs.002/1.18"

	timeLayout = "2006-01-02T15:04:05.000Z"
)

const (
	KindPacs008 = "pacs.008"
	KindPacs002 = "pacs.002"
	KindPacs004 = "pacs.004"
	KindCamt025 = "camt.025"
	KindAdmi002 = "admi.002"
)

const (
	StatusAccepted = "ACSP" // from the receiving participant
	StatusSettled  = "ACSC" // to the payer's participant
	StatusCredit   = "ACCC" // to the receiving participant, which can now credit
	StatusRejected = "RJCT"
)

type Party struct {
	ISPB        string
	Name        string // used for the payer only
	TaxID       string // CPF or CNPJ
	Branch      string
	Account     string
	AccountType AccountType
}

type PaymentOrder struct {
	MsgID       string // set when read
	EndToEndID  string
	AmountCents int64
	Payer       Party
	Receiver    Party
	Key         string // the Pix key; empty for a payment typed by account number
	Message     string // free text between users
	AcceptedAt  time.Time
}

func NewPacs008(ispb string, o PaymentOrder, now time.Time) (msgID string, body []byte, err error) {
	if o.AmountCents <= 0 {
		return "", nil, fmt.Errorf("%w: amount must be positive", ErrBadRequest)
	}
	if o.EndToEndID == "" {
		return "", nil, fmt.Errorf("%w: EndToEndId is required", ErrBadRequest)
	}
	if msgID, err = NewMessageID(ispb); err != nil {
		return "", nil, err
	}
	created := now.UTC().Format(timeLayout)
	accepted := o.AcceptedAt
	if accepted.IsZero() {
		accepted = now
	}

	tx := creditTransferXML{
		Amount:       amountXML{Currency: "BRL", Value: formatAmount(o.AmountCents)},
		AcceptedAt:   accepted.UTC().Format(timeLayout),
		ChrgBr:       "SLEV",
		Debtor:       debtorXML{Name: o.Payer.Name, ID: privateID(o.Payer.TaxID)},
		DebtorAcct:   bankAccount(o.Payer, ""),
		DebtorAgt:    agentOf(o.Payer.ISPB),
		CreditorAgt:  agentOf(o.Receiver.ISPB),
		Creditor:     creditorXML{ID: privateID(o.Receiver.TaxID)},
		CreditorAcct: bankAccount(o.Receiver, o.Key),
		Purpose:      purposeXML{Code: "IPAY"},
	}
	tx.PmtID.EndToEnd = o.EndToEndID
	tx.Mandate.Tp.LclInstrm.Prtry = "MANU"
	if o.Key != "" {
		tx.Mandate.Tp.LclInstrm.Prtry = "DICT"
	}
	if o.Message != "" {
		tx.Remittance = &remittanceXML{Text: o.Message}
	}

	m := pacs008XML{Xmlns: nsPacs008}
	m.AppHdr = newAppHdr(ispb, spiISPB, msgID, msgPacs008, created)
	g := &m.Document.FIToFICstmrCdtTrf
	g.GrpHdr.MsgID, g.GrpHdr.CreDtTm, g.GrpHdr.NbOfTxs = msgID, created, "1"
	g.GrpHdr.SttlmInf.Method = "CLRG"
	g.GrpHdr.PmtTpInf.Priority = "HIGH"
	g.GrpHdr.PmtTpInf.SvcLvl.Prtry = "PAGPRI"
	g.CdtTrfTxInf = []creditTransferXML{tx}

	body, err = marshalMessage(m)
	return msgID, body, err
}

func ParsePacs008(body []byte) ([]PaymentOrder, error) {
	var m pacs008XML
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("spi: invalid pacs.008: %w", err)
	}
	g := m.Document.FIToFICstmrCdtTrf
	out := make([]PaymentOrder, 0, len(g.CdtTrfTxInf))
	for _, tx := range g.CdtTrfTxInf {
		cents, err := parseAmount(tx.Amount.Value)
		if err != nil {
			return nil, err
		}
		accepted, _ := time.Parse(time.RFC3339, tx.AcceptedAt)
		o := PaymentOrder{
			MsgID:       g.GrpHdr.MsgID,
			EndToEndID:  tx.PmtID.EndToEnd,
			AmountCents: cents,
			Payer: Party{
				ISPB: tx.DebtorAgt.FinInstnId.ClrSysMmbId.MmbID, Name: tx.Debtor.Name, TaxID: tx.Debtor.ID.PrvtId.Othr.ID,
				Branch: tx.DebtorAcct.ID.Othr.Issr, Account: tx.DebtorAcct.ID.Othr.ID, AccountType: AccountType(tx.DebtorAcct.Tp.Cd),
			},
			Receiver: Party{
				ISPB: tx.CreditorAgt.FinInstnId.ClrSysMmbId.MmbID, TaxID: tx.Creditor.ID.PrvtId.Othr.ID,
				Branch: tx.CreditorAcct.ID.Othr.Issr, Account: tx.CreditorAcct.ID.Othr.ID, AccountType: AccountType(tx.CreditorAcct.Tp.Cd),
			},
			AcceptedAt: accepted,
		}
		if tx.CreditorAcct.Prxy != nil {
			o.Key = tx.CreditorAcct.Prxy.ID
		}
		if tx.Remittance != nil {
			o.Message = tx.Remittance.Text
		}
		out = append(out, o)
	}
	return out, nil
}

type TxStatus struct {
	InstrID    string // the EndToEndId of a payment, or the RtrId of a return
	EndToEndID string
	Status     string
	ReasonCode string // with StatusRejected
	Detail     string
	SettledAt  time.Time // filled by the SPI only
}

func (s TxStatus) Rejected() bool { return s.Status == StatusRejected }

func NewPacs002(ispb string, st TxStatus, now time.Time) (msgID string, body []byte, err error) {
	if st.EndToEndID == "" || st.Status == "" {
		return "", nil, fmt.Errorf("%w: EndToEndId and status are required", ErrBadRequest)
	}
	if st.Status == StatusRejected && st.ReasonCode == "" {
		return "", nil, fmt.Errorf("%w: a rejection needs a reason code", ErrBadRequest)
	}
	if msgID, err = NewMessageID(ispb); err != nil {
		return "", nil, err
	}
	created := now.UTC().Format(timeLayout)
	if st.InstrID == "" {
		st.InstrID = st.EndToEndID
	}
	tx := txStatusXML{OrgnlInstrID: st.InstrID, OrgnlEndToEnd: st.EndToEndID, Status: st.Status}
	if st.ReasonCode != "" {
		r := reasonXML{}
		r.Rsn.Cd = st.ReasonCode
		if st.Detail != "" {
			r.AddtlInf = []string{clip(st.Detail, 105)}
		}
		tx.Reason = []reasonXML{r}
	}

	m := pacs002XML{Xmlns: nsPacs002}
	m.AppHdr = newAppHdr(ispb, spiISPB, msgID, msgPacs002, created)
	m.Document.FIToFIPmtStsRpt.GrpHdr.MsgID = msgID
	m.Document.FIToFIPmtStsRpt.GrpHdr.CreDtTm = created
	m.Document.FIToFIPmtStsRpt.TxInfAndSts = []txStatusXML{tx}

	body, err = marshalMessage(m)
	return msgID, body, err
}

func ParsePacs002(body []byte) ([]TxStatus, error) {
	var m pacs002XML
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("spi: invalid pacs.002: %w", err)
	}
	var out []TxStatus
	for _, tx := range m.Document.FIToFIPmtStsRpt.TxInfAndSts {
		st := TxStatus{InstrID: tx.OrgnlInstrID, EndToEndID: tx.OrgnlEndToEnd, Status: tx.Status}
		if len(tx.Reason) > 0 {
			st.ReasonCode = tx.Reason[0].Rsn.Cd
			if len(tx.Reason[0].AddtlInf) > 0 {
				st.Detail = tx.Reason[0].AddtlInf[0]
			}
		}
		if tx.SettledAt != nil {
			st.SettledAt, _ = time.Parse(time.RFC3339, tx.SettledAt.DtTm)
		}
		out = append(out, st)
	}
	return out, nil
}

type Notification struct {
	Ref         string // the PI-ResourceId of the message that failed
	Reason      string
	Description string
}

func ParseAdmi002(body []byte) (Notification, error) {
	var m admi002XML
	if err := xml.Unmarshal(body, &m); err != nil {
		return Notification{}, fmt.Errorf("spi: invalid admi.002: %w", err)
	}
	a := m.Document.Admi
	return Notification{Ref: a.RltdRef.Ref, Reason: a.Rsn.Reason, Description: a.Rsn.Description}, nil
}

func MessageKind(body []byte) (string, error) {
	var env struct {
		AppHdr struct {
			MsgDefIdr string `xml:"MsgDefIdr"`
		} `xml:"AppHdr"`
	}
	if err := xml.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("spi: invalid XML: %w", err)
	}
	id := env.AppHdr.MsgDefIdr
	if id == "" {
		return "", errors.New("spi: message without AppHdr/MsgDefIdr")
	}
	if i := strings.Index(id, ".spi."); i > 0 {
		id = id[:i]
	}
	return id, nil
}

func formatAmount(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

func parseAmount(s string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("spi: invalid amount %q", s)
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || w < 0 || f < 0 {
		return 0, fmt.Errorf("spi: invalid amount %q", s)
	}
	return w*100 + f, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func marshalMessage(m any) ([]byte, error) {
	raw, err := xml.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("spi: encode message: %w", err)
	}
	return append([]byte(xml.Header), raw...), nil
}
