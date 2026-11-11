package httpapi

import (
	"encoding/xml"
	"time"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// isoTime serializa datas em UTC com milissegundos, como nos exemplos da spec.
type isoTime time.Time

func (t isoTime) MarshalText() ([]byte, error) {
	return []byte(time.Time(t).UTC().Format(timeLayout)), nil
}

func (t *isoTime) UnmarshalText(b []byte) error {
	parsed, err := time.Parse(time.RFC3339, string(b))
	if err != nil {
		return err
	}
	*t = isoTime(parsed)
	return nil
}

type accountXML struct {
	Participant   string  `xml:"Participant"`
	Branch        string  `xml:"Branch,omitempty"`
	AccountNumber string  `xml:"AccountNumber"`
	AccountType   string  `xml:"AccountType"`
	OpeningDate   isoTime `xml:"OpeningDate"`
}

type ownerXML struct {
	Type        string `xml:"Type"`
	TaxIDNumber string `xml:"TaxIdNumber"`
	Name        string `xml:"Name"`
	TradeName   string `xml:"TradeName,omitempty"`
}

// entryXML serve para requisições (CreationDate/KeyOwnershipDate ignorados) e respostas.
type entryXML struct {
	Key              string     `xml:"Key,omitempty"`
	KeyType          string     `xml:"KeyType"`
	Account          accountXML `xml:"Account"`
	Owner            ownerXML   `xml:"Owner"`
	CreationDate     *isoTime   `xml:"CreationDate,omitempty"`
	KeyOwnershipDate *isoTime   `xml:"KeyOwnershipDate,omitempty"`
}

// --- Requisições ---

type createEntryRequest struct {
	XMLName   xml.Name `xml:"CreateEntryRequest"`
	Entry     entryXML `xml:"Entry"`
	Reason    string   `xml:"Reason"`
	RequestID string   `xml:"RequestId"`
}

type updateEntryRequest struct {
	XMLName xml.Name    `xml:"UpdateEntryRequest"`
	Key     string      `xml:"Key"`
	Account *accountXML `xml:"Account"`
	Owner   *ownerXML   `xml:"Owner"`
	Reason  string      `xml:"Reason"`
}

type deleteEntryRequest struct {
	XMLName     xml.Name `xml:"DeleteEntryRequest"`
	Key         string   `xml:"Key"`
	Participant string   `xml:"Participant"`
	Reason      string   `xml:"Reason"`
}

type checkKeysRequest struct {
	XMLName xml.Name `xml:"CheckKeysRequest"`
	Keys    struct {
		Key []string `xml:"Key"`
	} `xml:"Keys"`
}

// --- Respostas ---

// header reúne os campos comuns a todas as respostas do DICT.
type header struct {
	Signature     string  `xml:"Signature"` // o mock não assina: elemento vazio
	ResponseTime  isoTime `xml:"ResponseTime"`
	CorrelationID string  `xml:"CorrelationId"`
}

type createEntryResponse struct {
	XMLName xml.Name `xml:"CreateEntryResponse"`
	header
	Entry entryXML `xml:"Entry"`
}

type updateEntryResponse struct {
	XMLName xml.Name `xml:"UpdateEntryResponse"`
	header
	Entry entryXML `xml:"Entry"`
}

type getEntryResponse struct {
	XMLName xml.Name `xml:"GetEntryResponse"`
	header
	Entry           entryXML       `xml:"Entry"`
	OwnerStatistics *ownerStatsXML `xml:"OwnerStatistics,omitempty"`
	KeyStatistics   *keyStatsXML   `xml:"KeyStatistics,omitempty"`
}

type deleteEntryResponse struct {
	XMLName xml.Name `xml:"DeleteEntryResponse"`
	header
	Key string `xml:"Key"`
}

type keyStatusXML struct {
	Key      string `xml:",chardata"`
	HasEntry bool   `xml:"hasEntry,attr"`
}

type checkKeysResponse struct {
	XMLName xml.Name `xml:"CheckKeysResponse"`
	header
	Keys struct {
		Key []keyStatusXML `xml:"Key"`
	} `xml:"Keys"`
}

// --- Estatísticas antifraude (sempre zeradas no mock) ---

type windowed struct {
	D90 int `xml:"d90,attr"`
	M12 int `xml:"m12,attr"`
	M60 int `xml:"m60,attr"`
}

type commonStats struct {
	Spi struct {
		Watermark   isoTime  `xml:"Watermark"`
		Settlements windowed `xml:"Settlements"`
	} `xml:"Spi"`
	FraudMarkers struct {
		Watermark                   isoTime  `xml:"Watermark"`
		ApplicationFrauds           windowed `xml:"ApplicationFrauds"`
		MuleAccounts                windowed `xml:"MuleAccounts"`
		ScammerAccounts             windowed `xml:"ScammerAccounts"`
		OtherFrauds                 windowed `xml:"OtherFrauds"`
		UnknownFrauds               windowed `xml:"UnknownFrauds"`
		TotalFraudTransactionAmount windowed `xml:"TotalFraudTransactionAmount"`
		DistinctFraudReporters      windowed `xml:"DistinctFraudReporters"`
	} `xml:"FraudMarkers"`
	InfractionReports struct {
		Watermark                    isoTime  `xml:"Watermark"`
		OpenReports                  int      `xml:"OpenReports"`
		OpenReportsDistinctReporters int      `xml:"OpenReportsDistinctReporters"`
		RejectedReports              windowed `xml:"RejectedReports"`
	} `xml:"InfractionReports"`
}

type ownerStatsXML struct {
	commonStats
	Entries struct {
		Watermark          isoTime `xml:"Watermark"`
		RegisteredAccounts int     `xml:"RegisteredAccounts"`
	} `xml:"Entries"`
}

type keyStatsXML struct {
	commonStats
	Entries struct {
		Watermark        isoTime  `xml:"Watermark"`
		DistinctAccounts windowed `xml:"DistinctAccounts"`
	} `xml:"Entries"`
}

func zeroStats(now time.Time) (*ownerStatsXML, *keyStatsXML) {
	w := isoTime(now)
	var o ownerStatsXML
	o.Spi.Watermark, o.FraudMarkers.Watermark, o.InfractionReports.Watermark, o.Entries.Watermark = w, w, w, w
	var k keyStatsXML
	k.Spi.Watermark, k.FraudMarkers.Watermark, k.InfractionReports.Watermark, k.Entries.Watermark = w, w, w, w
	return &o, &k
}
