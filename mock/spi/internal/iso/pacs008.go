package iso

import (
	"encoding/xml"
	"fmt"
)

// Pacs008 is a customer credit transfer (the payment order).
type Pacs008 struct {
	XMLName  xml.Name `xml:"Envelope"`
	Xmlns    string   `xml:"xmlns,attr"`
	AppHdr   AppHdr   `xml:"AppHdr"`
	Document struct {
		FIToFICstmrCdtTrf struct {
			GrpHdr      GroupHeader      `xml:"GrpHdr"`
			CdtTrfTxInf []CreditTransfer `xml:"CdtTrfTxInf"`
		} `xml:"FIToFICstmrCdtTrf"`
	} `xml:"Document"`
}

// GroupHeader holds what the transactions of a pacs.008 have in common.
type GroupHeader struct {
	MsgID    string `xml:"MsgId"`
	CreDtTm  string `xml:"CreDtTm"`
	NbOfTxs  string `xml:"NbOfTxs"`
	SttlmInf struct {
		Method string `xml:"SttlmMtd"`
	} `xml:"SttlmInf"`
	PmtTpInf struct {
		Priority string `xml:"InstrPrty"`
		SvcLvl   struct {
			Proprietary string `xml:"Prtry"`
		} `xml:"SvcLvl"`
	} `xml:"PmtTpInf"`
}

// CreditTransfer is one transaction of a pacs.008. Elements the mock does not support
// (InitgPty, Tax, structured remittance) are kept as raw XML, only to be refused.
type CreditTransfer struct {
	PmtID struct {
		InstrID  string `xml:"InstrId,omitempty"`
		EndToEnd string `xml:"EndToEndId"`
		TxID     string `xml:"TxId,omitempty"`
	} `xml:"PmtId"`
	Amount     Amount `xml:"IntrBkSttlmAmt"`
	AcceptedAt string `xml:"AccptncDtTm"`
	ChrgBr     string `xml:"ChrgBr"`
	Mandate    struct {
		Tp struct {
			LclInstrm struct {
				Prtry string `xml:"Prtry"`
			} `xml:"LclInstrm"`
		} `xml:"Tp"`
	} `xml:"MndtRltdInf"`
	InitgPty *RawXML `xml:"InitgPty,omitempty"`
	Debtor   struct {
		Name string `xml:"Nm"`
		ID   PrvtID `xml:"Id"`
	} `xml:"Dbtr"`
	DebtorAcct    Account `xml:"DbtrAcct"`
	DebtorAgent   Agent   `xml:"DbtrAgt"`
	CreditorAgent Agent   `xml:"CdtrAgt"`
	Creditor      struct {
		ID PrvtID `xml:"Id"`
	} `xml:"Cdtr"`
	CreditorAcct Account `xml:"CdtrAcct"`
	Purpose      struct {
		Cd string `xml:"Cd"`
	} `xml:"Purp"`
	Tax    *RawXML `xml:"Tax,omitempty"`
	RmtInf *struct {
		Ustrd string  `xml:"Ustrd,omitempty"`
		Strd  *RawXML `xml:"Strd,omitempty"`
	} `xml:"RmtInf,omitempty"`
}

// RawXML keeps an element's content untouched.
type RawXML struct {
	Inner string `xml:",innerxml"`
}

// Amount is a monetary amount with its currency attribute.
type Amount struct {
	Ccy   string `xml:"Ccy,attr"`
	Value string `xml:",chardata"`
}

// PrvtID is the CPF or CNPJ of a party (Id/PrvtId/Othr/Id).
type PrvtID struct {
	PrvtID struct {
		Othr struct {
			ID string `xml:"Id"`
		} `xml:"Othr"`
	} `xml:"PrvtId"`
}

// TaxID returns the CPF or CNPJ.
func (p PrvtID) TaxID() string { return p.PrvtID.Othr.ID }

func newPrvtID(id string) PrvtID {
	var p PrvtID
	p.PrvtID.Othr.ID = id
	return p
}

// Account is a transactional account (Id/Othr/{Id,Issr}, Tp/Cd, and the Pix key in Prxy).
type Account struct {
	ID struct {
		Othr struct {
			Number string `xml:"Id"`
			Branch string `xml:"Issr,omitempty"`
		} `xml:"Othr"`
	} `xml:"Id"`
	Tp struct {
		Cd string `xml:"Cd"`
	} `xml:"Tp"`
	Prxy *Proxy `xml:"Prxy,omitempty"`
}

// Proxy holds the Pix key used to find the account.
type Proxy struct {
	ID string `xml:"Id"`
}

// Number returns the account number.
func (a Account) Number() string { return a.ID.Othr.Number }

// Branch returns the branch, empty when the account has none.
func (a Account) Branch() string { return a.ID.Othr.Branch }

// Type returns the account type code (CACC, SVGS, TRAN...).
func (a Account) Type() string { return a.Tp.Cd }

// Key returns the Pix key in the proxy block, empty when absent.
func (a Account) Key() string {
	if a.Prxy == nil {
		return ""
	}
	return a.Prxy.ID
}

// Agent is the participant that holds an account (FinInstnId/ClrSysMmbId/MmbId).
type Agent struct {
	FinInstnID struct {
		ClrSysMmbID struct {
			MmbID string `xml:"MmbId"`
		} `xml:"ClrSysMmbId"`
	} `xml:"FinInstnId"`
}

// ISPB returns the participant code.
func (a Agent) ISPB() string { return a.FinInstnID.ClrSysMmbID.MmbID }

func newAgent(ispb string) Agent {
	var a Agent
	a.FinInstnID.ClrSysMmbID.MmbID = ispb
	return a
}

// ParsePacs008 reads a pacs.008 message.
func ParsePacs008(body []byte) (*Pacs008, error) {
	var m Pacs008
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid pacs.008: %w", err)
	}
	return &m, nil
}

// Transactions returns the transactions of the message.
func (m *Pacs008) Transactions() []CreditTransfer { return m.Document.FIToFICstmrCdtTrf.CdtTrfTxInf }

// Group returns the group header.
func (m *Pacs008) Group() GroupHeader { return m.Document.FIToFICstmrCdtTrf.GrpHdr }

// NewPacs008 builds a message carrying one transaction, as the SPI forwards it to the receiver.
func NewPacs008(from, to, msgID, createdAt string, priority, level string, tx CreditTransfer) *Pacs008 {
	m := &Pacs008{Xmlns: nsPacs008}
	m.AppHdr = AppHdr{Fr: NewParty(from), To: NewParty(to), BizMsgIdr: msgID, MsgDefIdr: Pacs008ID, CreDt: createdAt}
	g := &m.Document.FIToFICstmrCdtTrf.GrpHdr
	g.MsgID, g.CreDtTm, g.NbOfTxs = msgID, createdAt, "1"
	g.SttlmInf.Method = "CLRG"
	g.PmtTpInf.Priority, g.PmtTpInf.SvcLvl.Proprietary = priority, level
	m.Document.FIToFICstmrCdtTrf.CdtTrfTxInf = []CreditTransfer{tx}
	return m
}

// Marshal returns the message as XML with the declaration.
func (m *Pacs008) Marshal() ([]byte, error) { return marshal(m) }

func marshal(v any) ([]byte, error) {
	out, err := xml.MarshalIndent(v, "", "    ")
	if err != nil {
		return nil, err
	}
	return []byte(xml.Header + string(out)), nil
}

// NewCreditTransfer starts a transaction. Fill the parties with SetDebtor and SetCreditor.
func NewCreditTransfer(e2e, amount, acceptedAt string) CreditTransfer {
	var tx CreditTransfer
	tx.PmtID.EndToEnd = e2e
	tx.Amount = Amount{Ccy: "BRL", Value: amount}
	tx.AcceptedAt = acceptedAt
	tx.ChrgBr = "SLEV"
	tx.Purpose.Cd = "IPAY"
	return tx
}

// SetDebtor fills the payer side of a transaction.
func (tx *CreditTransfer) SetDebtor(name, taxID, ispb, branch, number, accType string) {
	tx.Debtor.Name = name
	tx.Debtor.ID = newPrvtID(taxID)
	tx.DebtorAgent = newAgent(ispb)
	tx.DebtorAcct.ID.Othr.Number, tx.DebtorAcct.ID.Othr.Branch, tx.DebtorAcct.Tp.Cd = number, branch, accType
}

// SetCreditor fills the receiver side of a transaction. key is empty for manual entry (MANU).
func (tx *CreditTransfer) SetCreditor(taxID, ispb, branch, number, accType, key string) {
	tx.Creditor.ID = newPrvtID(taxID)
	tx.CreditorAgent = newAgent(ispb)
	tx.CreditorAcct.ID.Othr.Number, tx.CreditorAcct.ID.Othr.Branch, tx.CreditorAcct.Tp.Cd = number, branch, accType
	tx.Mandate.Tp.LclInstrm.Prtry = "MANU"
	if key != "" {
		tx.CreditorAcct.Prxy = &Proxy{ID: key}
		tx.Mandate.Tp.LclInstrm.Prtry = "DICT"
	}
}
