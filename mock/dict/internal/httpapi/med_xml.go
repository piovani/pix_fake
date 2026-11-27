package httpapi

import "encoding/xml"

// --- MED: infraction reports, fraud markers, refunds ---

type contactXML struct {
	Email string `xml:"Email"`
	Phone string `xml:"Phone"`
}

// infractionReportXML is the report of a request, and of a response with the fields only the DICT fills.
type infractionReportXML struct {
	TransactionID           string      `xml:"TransactionId"`
	Reason                  string      `xml:"Reason"`
	SituationType           string      `xml:"SituationType"`
	ReportDetails           string      `xml:"ReportDetails,omitempty"`
	FundsRecoveryID         string      `xml:"FundsRecoveryId,omitempty"`
	TransactionDepth        int         `xml:"TransactionDepth,omitempty"`
	ID                      string      `xml:"Id,omitempty"`
	Status                  string      `xml:"Status,omitempty"`
	ReporterParticipant     string      `xml:"ReporterParticipant,omitempty"`
	CounterpartyParticipant string      `xml:"CounterpartyParticipant,omitempty"`
	FraudMarkerID           string      `xml:"FraudMarkerId,omitempty"`
	AnalysisResult          string      `xml:"AnalysisResult,omitempty"`
	AnalysisDetails         string      `xml:"AnalysisDetails,omitempty"`
	ContactInformation      *contactXML `xml:"ContactInformation,omitempty"`
	InfractionAmount        string      `xml:"InfractionAmount,omitempty"`
	CreationTime            *isoTime    `xml:"CreationTime,omitempty"`
	LastModified            *isoTime    `xml:"LastModified,omitempty"`
}

type createInfractionReportRequest struct {
	XMLName          xml.Name            `xml:"CreateInfractionReportRequest"`
	Participant      string              `xml:"Participant"`
	InfractionReport infractionReportXML `xml:"InfractionReport"`
}

type updateInfractionReportRequest struct {
	XMLName            xml.Name    `xml:"UpdateInfractionReportRequest"`
	InfractionReportID string      `xml:"InfractionReportId"`
	Participant        string      `xml:"Participant"`
	SituationType      string      `xml:"SituationType"`
	ContactInformation *contactXML `xml:"ContactInformation"`
	ReportDetails      *string     `xml:"ReportDetails"`
}

// reportActionRequest is the body of acknowledge and cancel; their root elements differ.
type reportActionRequest struct {
	XMLName            xml.Name
	InfractionReportID string `xml:"InfractionReportId"`
	Participant        string `xml:"Participant"`
}

type closeInfractionReportRequest struct {
	XMLName            xml.Name `xml:"CloseInfractionReportRequest"`
	InfractionReportID string   `xml:"InfractionReportId"`
	Participant        string   `xml:"Participant"`
	FraudType          string   `xml:"FraudType"`
	AnalysisResult     string   `xml:"AnalysisResult"`
	AnalysisDetails    string   `xml:"AnalysisDetails"`
}

// infractionReportResponse is the response of every operation that answers one report; only the root element changes.
type infractionReportResponse struct {
	XMLName xml.Name
	header
	InfractionReport infractionReportXML `xml:"InfractionReport"`
}

type listInfractionReportsResponse struct {
	XMLName xml.Name `xml:"ListInfractionReportsResponse"`
	header
	HasMoreElements   bool                  `xml:"HasMoreElements"`
	InfractionReports []infractionReportXML `xml:"InfractionReports>InfractionReport"`
}

type fraudMarkerReportXML struct {
	ID                  string `xml:"Id"`
	ReporterParticipant string `xml:"ReporterParticipant,omitempty"`
}

type fraudMarkerXML struct {
	ID               string                `xml:"Id,omitempty"`
	Status           string                `xml:"Status,omitempty"`
	TaxIDNumber      string                `xml:"TaxIdNumber"`
	FraudType        string                `xml:"FraudType"`
	Key              string                `xml:"Key,omitempty"`
	CreationTime     *isoTime              `xml:"CreationTime,omitempty"`
	LastModified     *isoTime              `xml:"LastModified,omitempty"`
	InfractionReport *fraudMarkerReportXML `xml:"InfractionReport,omitempty"`
}

type createFraudMarkerRequest struct {
	XMLName     xml.Name       `xml:"CreateFraudMarkerRequest"`
	Participant string         `xml:"Participant"`
	FraudMarker fraudMarkerXML `xml:"FraudMarker"`
	RequestID   string         `xml:"RequestId"`
}

type cancelFraudMarkerRequest struct {
	XMLName       xml.Name `xml:"CancelFraudMarkerRequest"`
	FraudMarkerID string   `xml:"FraudMarkerId"`
	Participant   string   `xml:"Participant"`
}

type fraudMarkerResponse struct {
	XMLName xml.Name
	header
	FraudMarker fraudMarkerXML `xml:"FraudMarker"`
}

type listFraudMarkersResponse struct {
	XMLName xml.Name `xml:"ListFraudMarkersResponse"`
	header
	HasMoreElements bool             `xml:"HasMoreElements"`
	Total           int              `xml:"Total"`
	FraudMarkers    []fraudMarkerXML `xml:"FraudMarkers>FraudMarker"`
}

type refundXML struct {
	TransactionID         string   `xml:"TransactionId"`
	RefundReason          string   `xml:"RefundReason"`
	RefundAmount          string   `xml:"RefundAmount"`
	RefundDetails         string   `xml:"RefundDetails,omitempty"`
	FundsRecoveryID       string   `xml:"FundsRecoveryId,omitempty"`
	ID                    string   `xml:"Id,omitempty"`
	Status                string   `xml:"Status,omitempty"`
	ContestedParticipant  string   `xml:"ContestedParticipant,omitempty"`
	RequestingParticipant string   `xml:"RequestingParticipant,omitempty"`
	InfractionReportID    string   `xml:"InfractionReportId,omitempty"`
	AnalysisResult        string   `xml:"AnalysisResult,omitempty"`
	AnalysisDetails       string   `xml:"AnalysisDetails,omitempty"`
	RefundRejectionReason string   `xml:"RefundRejectionReason,omitempty"`
	RefundTransactionID   string   `xml:"RefundTransactionId,omitempty"`
	CreationTime          *isoTime `xml:"CreationTime,omitempty"`
	LastModified          *isoTime `xml:"LastModified,omitempty"`
}

type createRefundRequest struct {
	XMLName     xml.Name  `xml:"CreateRefundRequest"`
	Participant string    `xml:"Participant"`
	Refund      refundXML `xml:"Refund"`
}

type cancelRefundRequest struct {
	XMLName     xml.Name `xml:"CancelRefundRequest"`
	RefundID    string   `xml:"RefundId"`
	Participant string   `xml:"Participant"`
}

type closeRefundRequest struct {
	XMLName               xml.Name `xml:"CloseRefundRequest"`
	RefundID              string   `xml:"RefundId"`
	Participant           string   `xml:"Participant"`
	RefundAnalysisResult  string   `xml:"RefundAnalysisResult"`
	RefundAnalysisDetails string   `xml:"RefundAnalysisDetails"`
	RefundRejectionReason string   `xml:"RefundRejectionReason"`
	RefundTransactionID   string   `xml:"RefundTransactionId"`
}

type refundResponse struct {
	XMLName xml.Name
	header
	Refund refundXML `xml:"Refund"`
}

type listRefundsResponse struct {
	XMLName xml.Name `xml:"ListRefundsResponse"`
	header
	HasMoreElements bool        `xml:"HasMoreElements"`
	NextCursor      string      `xml:"NextCursor,omitempty"`
	Refunds         []refundXML `xml:"Refunds>Refund"`
}

type getPersonStatisticsResponse struct {
	XMLName xml.Name `xml:"GetPersonStatisticsResponse"`
	header
	TaxIDNumber      string         `xml:"TaxIdNumber"`
	PersonStatistics *ownerStatsXML `xml:"PersonStatistics"`
}

type getEntryStatisticsResponse struct {
	XMLName xml.Name `xml:"GetEntryStatisticsResponse"`
	header
	Key             string         `xml:"Key"`
	OwnerStatistics *ownerStatsXML `xml:"OwnerStatistics"`
	KeyStatistics   *keyStatsXML   `xml:"KeyStatistics"`
}
