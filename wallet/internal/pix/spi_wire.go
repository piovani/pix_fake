package pix

import "encoding/xml"

type partyXML struct {
	FIId struct {
		FinInstnId struct {
			Othr struct {
				ID string `xml:"Id"`
			} `xml:"Othr"`
		} `xml:"FinInstnId"`
	} `xml:"FIId"`
}

type appHdrXML struct {
	Fr        partyXML `xml:"Fr"`
	To        partyXML `xml:"To"`
	BizMsgIdr string   `xml:"BizMsgIdr"`
	MsgDefIdr string   `xml:"MsgDefIdr"`
	CreDt     string   `xml:"CreDt"`
	Sgntr     struct{} `xml:"Sgntr"`
}

func newAppHdr(from, to, msgID, msgDef, created string) appHdrXML {
	h := appHdrXML{BizMsgIdr: msgID, MsgDefIdr: msgDef, CreDt: created}
	h.Fr.FIId.FinInstnId.Othr.ID = from
	h.To.FIId.FinInstnId.Othr.ID = to
	return h
}

type privateIDXML struct {
	PrvtId struct {
		Othr struct {
			ID string `xml:"Id"`
		} `xml:"Othr"`
	} `xml:"PrvtId"`
}

func privateID(taxID string) privateIDXML {
	var p privateIDXML
	p.PrvtId.Othr.ID = taxID
	return p
}

type agentXML struct {
	FinInstnId struct {
		ClrSysMmbId struct {
			MmbID string `xml:"MmbId"`
		} `xml:"ClrSysMmbId"`
	} `xml:"FinInstnId"`
}

func agentOf(ispb string) agentXML {
	var a agentXML
	a.FinInstnId.ClrSysMmbId.MmbID = ispb
	return a
}

type accountIDXML struct {
	ID   string `xml:"Id"`
	Issr string `xml:"Issr,omitempty"`
}

type bankAccountXML struct {
	ID struct {
		Othr accountIDXML `xml:"Othr"`
	} `xml:"Id"`
	Tp struct {
		Cd string `xml:"Cd"`
	} `xml:"Tp"`
	Prxy *struct {
		ID string `xml:"Id"`
	} `xml:"Prxy,omitempty"`
}

func bankAccount(p Party, key string) bankAccountXML {
	var a bankAccountXML
	a.ID.Othr = accountIDXML{ID: p.Account, Issr: p.Branch}
	a.Tp.Cd = string(p.AccountType)
	if key != "" {
		a.Prxy = &struct {
			ID string `xml:"Id"`
		}{ID: key}
	}
	return a
}

type amountXML struct {
	Currency string `xml:"Ccy,attr"`
	Value    string `xml:",chardata"`
}

type debtorXML struct {
	Name string       `xml:"Nm"`
	ID   privateIDXML `xml:"Id"`
}

type creditorXML struct {
	ID privateIDXML `xml:"Id"`
}

type purposeXML struct {
	Code string `xml:"Cd"`
}

type remittanceXML struct {
	Text string `xml:"Ustrd"`
}

type creditTransferXML struct {
	PmtID struct {
		EndToEnd string `xml:"EndToEndId"`
	} `xml:"PmtId"`
	Amount     amountXML `xml:"IntrBkSttlmAmt"`
	AcceptedAt string    `xml:"AccptncDtTm"`
	ChrgBr     string    `xml:"ChrgBr"`
	Mandate    struct {
		Tp struct {
			LclInstrm struct {
				Prtry string `xml:"Prtry"`
			} `xml:"LclInstrm"`
		} `xml:"Tp"`
	} `xml:"MndtRltdInf"`
	Debtor       debtorXML      `xml:"Dbtr"`
	DebtorAcct   bankAccountXML `xml:"DbtrAcct"`
	DebtorAgt    agentXML       `xml:"DbtrAgt"`
	CreditorAgt  agentXML       `xml:"CdtrAgt"`
	Creditor     creditorXML    `xml:"Cdtr"`
	CreditorAcct bankAccountXML `xml:"CdtrAcct"`
	Purpose      purposeXML     `xml:"Purp"`
	Remittance   *remittanceXML `xml:"RmtInf,omitempty"`
}

type pacs008XML struct {
	XMLName  xml.Name  `xml:"Envelope"`
	Xmlns    string    `xml:"xmlns,attr"`
	AppHdr   appHdrXML `xml:"AppHdr"`
	Document struct {
		FIToFICstmrCdtTrf struct {
			GrpHdr struct {
				MsgID    string `xml:"MsgId"`
				CreDtTm  string `xml:"CreDtTm"`
				NbOfTxs  string `xml:"NbOfTxs"`
				SttlmInf struct {
					Method string `xml:"SttlmMtd"`
				} `xml:"SttlmInf"`
				PmtTpInf struct {
					Priority string `xml:"InstrPrty"`
					SvcLvl   struct {
						Prtry string `xml:"Prtry"`
					} `xml:"SvcLvl"`
				} `xml:"PmtTpInf"`
			} `xml:"GrpHdr"`
			CdtTrfTxInf []creditTransferXML `xml:"CdtTrfTxInf"`
		} `xml:"FIToFICstmrCdtTrf"`
	} `xml:"Document"`
}

type reasonXML struct {
	Rsn struct {
		Cd string `xml:"Cd"`
	} `xml:"Rsn"`
	AddtlInf []string `xml:"AddtlInf,omitempty"`
}

type txStatusXML struct {
	OrgnlInstrID  string      `xml:"OrgnlInstrId"`
	OrgnlEndToEnd string      `xml:"OrgnlEndToEndId"`
	Status        string      `xml:"TxSts"`
	Reason        []reasonXML `xml:"StsRsnInf,omitempty"`
	SettledAt     *struct {
		DtTm string `xml:"DtTm"`
	} `xml:"FctvIntrBkSttlmDt,omitempty"`
}

type pacs002XML struct {
	XMLName  xml.Name  `xml:"Envelope"`
	Xmlns    string    `xml:"xmlns,attr"`
	AppHdr   appHdrXML `xml:"AppHdr"`
	Document struct {
		FIToFIPmtStsRpt struct {
			GrpHdr struct {
				MsgID   string `xml:"MsgId"`
				CreDtTm string `xml:"CreDtTm"`
			} `xml:"GrpHdr"`
			TxInfAndSts []txStatusXML `xml:"TxInfAndSts"`
		} `xml:"FIToFIPmtStsRpt"`
	} `xml:"Document"`
}

type admi002XML struct {
	Document struct {
		Admi struct {
			RltdRef struct {
				Ref string `xml:"Ref"`
			} `xml:"RltdRef"`
			Rsn struct {
				Reason      string `xml:"RjctgPtyRsn"`
				Description string `xml:"RsnDesc"`
			} `xml:"Rsn"`
		} `xml:"admi.002.001.01"`
	} `xml:"Document"`
}
