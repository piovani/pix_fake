package pix

import "encoding/xml"

func xmlName(local string) xml.Name { return xml.Name{Local: local} }

type reportXML struct {
	ID                      string `xml:"Id"`
	TransactionID           string `xml:"TransactionId"`
	Reason                  string `xml:"Reason"`
	SituationType           string `xml:"SituationType"`
	ReportDetails           string `xml:"ReportDetails"`
	Status                  string `xml:"Status"`
	ReporterParticipant     string `xml:"ReporterParticipant"`
	CounterpartyParticipant string `xml:"CounterpartyParticipant"`
	AnalysisResult          string `xml:"AnalysisResult"`
	FundsRecoveryID         string `xml:"FundsRecoveryId"`
	InfractionAmount        string `xml:"InfractionAmount"`
	LastModified            string `xml:"LastModified"`
}

func (r reportXML) report() (InfractionReport, error) {
	amount, err := parseMEDAmount(r.InfractionAmount)
	if err != nil {
		return InfractionReport{}, err
	}
	modified, err := parseMEDTime(r.LastModified)
	if err != nil {
		return InfractionReport{}, err
	}
	return InfractionReport{
		ID: r.ID, TransactionID: r.TransactionID, Reason: r.Reason, SituationType: r.SituationType, Details: r.ReportDetails,
		Status: r.Status, Reporter: r.ReporterParticipant, Counterparty: r.CounterpartyParticipant, AnalysisResult: r.AnalysisResult,
		FundsRecoveryID: r.FundsRecoveryID, AmountCents: amount, Modified: modified,
	}, nil
}

type listReportsXML struct {
	HasMore bool        `xml:"HasMoreElements"`
	Reports []reportXML `xml:"InfractionReports>InfractionReport"`
}

type reportResponseXML struct {
	Report reportXML `xml:"InfractionReport"`
}

type reportActionXML struct {
	XMLName     xml.Name
	ID          string `xml:"InfractionReportId"`
	Participant string `xml:"Participant"`
}

type closeReportXML struct {
	XMLName         xml.Name `xml:"CloseInfractionReportRequest"`
	ID              string   `xml:"InfractionReportId"`
	Participant     string   `xml:"Participant"`
	FraudType       string   `xml:"FraudType,omitempty"`
	AnalysisResult  string   `xml:"AnalysisResult"`
	AnalysisDetails string   `xml:"AnalysisDetails,omitempty"`
}

type refundXML struct {
	ID                    string `xml:"Id"`
	TransactionID         string `xml:"TransactionId"`
	RefundReason          string `xml:"RefundReason"`
	RefundAmount          string `xml:"RefundAmount"`
	RefundDetails         string `xml:"RefundDetails"`
	Status                string `xml:"Status"`
	RequestingParticipant string `xml:"RequestingParticipant"`
	ContestedParticipant  string `xml:"ContestedParticipant"`
	InfractionReportID    string `xml:"InfractionReportId"`
	FundsRecoveryID       string `xml:"FundsRecoveryId"`
	LastModified          string `xml:"LastModified"`
}

func (r refundXML) refund() (Refund, error) {
	amount, err := parseMEDAmount(r.RefundAmount)
	if err != nil {
		return Refund{}, err
	}
	modified, err := parseMEDTime(r.LastModified)
	if err != nil {
		return Refund{}, err
	}
	return Refund{
		ID: r.ID, TransactionID: r.TransactionID, Reason: r.RefundReason, AmountCents: amount, Details: r.RefundDetails,
		Status: r.Status, Requesting: r.RequestingParticipant, Contested: r.ContestedParticipant, ReportID: r.InfractionReportID,
		FundsRecoveryID: r.FundsRecoveryID, Modified: modified,
	}, nil
}

type listRefundsXML struct {
	HasMore    bool        `xml:"HasMoreElements"`
	NextCursor string      `xml:"NextCursor"`
	Refunds    []refundXML `xml:"Refunds>Refund"`
}

type refundResponseXML struct {
	Refund refundXML `xml:"Refund"`
}

type closeRefundXML struct {
	XMLName             xml.Name `xml:"CloseRefundRequest"`
	ID                  string   `xml:"RefundId"`
	Participant         string   `xml:"Participant"`
	Result              string   `xml:"RefundAnalysisResult"`
	Details             string   `xml:"RefundAnalysisDetails,omitempty"`
	RejectionReason     string   `xml:"RefundRejectionReason,omitempty"`
	RefundTransactionID string   `xml:"RefundTransactionId,omitempty"`
}
