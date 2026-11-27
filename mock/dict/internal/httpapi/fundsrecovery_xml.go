package httpapi

import "encoding/xml"

// --- MED: funds recoveries and event notifications ---

// fundsRecoveryXML is the FundsRecovery of a request, and the ExtendedFundsRecovery of a response.
type fundsRecoveryXML struct {
	FlowType            string      `xml:"FlowType,omitempty"`
	RootTransactionID   string      `xml:"RootTransactionId"`
	SituationType       string      `xml:"SituationType"`
	ReportDetails       string      `xml:"ReportDetails,omitempty"`
	ContactInformation  *contactXML `xml:"ContactInformation"`
	ID                  string      `xml:"Id,omitempty"`
	Status              string      `xml:"Status,omitempty"`
	ReporterParticipant string      `xml:"ReporterParticipant,omitempty"`
	CreationTime        *isoTime    `xml:"CreationTime,omitempty"`
	LastModified        *isoTime    `xml:"LastModified,omitempty"`
}

// graphParametersXML is kept as text, so that a value that does not parse is reported as a violation.
type graphParametersXML struct {
	MinTransactionAmount string `xml:"MinTransactionAmount,omitempty"`
	MaxTransactions      string `xml:"MaxTransactions,omitempty"`
	HopWindow            string `xml:"HopWindow,omitempty"`
	MaxHops              string `xml:"MaxHops,omitempty"`
}

type graphPersonXML struct {
	ID   int    `xml:"Id"`
	Type string `xml:"Type"`
}

type graphAccountXML struct {
	ID          int    `xml:"Id"`
	OwnerID     int    `xml:"OwnerId"`
	Participant string `xml:"Participant"`
}

type graphTransactionXML struct {
	ID                string  `xml:"Id"`
	DebtorAccountID   int     `xml:"DebtorAccountId"`
	CreditorAccountID int     `xml:"CreditorAccountId"`
	Amount            string  `xml:"Amount"`
	RefundableAmount  string  `xml:"RefundableAmount"`
	SettlementTime    isoTime `xml:"SettlementTime"`
	TransactionDepth  int     `xml:"TransactionDepth"`
}

type trackingGraphXML struct {
	FundsRecoveryID   string                `xml:"FundsRecoveryId"`
	RootTransactionID string                `xml:"RootTransactionId"`
	CreationTime      isoTime               `xml:"CreationTime"`
	Parameters        graphParametersXML    `xml:"Parameters"`
	Persons           []graphPersonXML      `xml:"Persons>Person"`
	Accounts          []graphAccountXML     `xml:"Accounts>Account"`
	Transactions      []graphTransactionXML `xml:"Transactions>Transaction"`
}

type createFundsRecoveryRequest struct {
	XMLName                 xml.Name            `xml:"CreateFundsRecoveryRequest"`
	Participant             string              `xml:"Participant"`
	FundsRecovery           fundsRecoveryXML    `xml:"FundsRecovery"`
	TrackingGraphParameters *graphParametersXML `xml:"TrackingGraphParameters"`
}

type updateFundsRecoveryRequest struct {
	XMLName            xml.Name    `xml:"UpdateFundsRecoveryRequest"`
	FundsRecoveryID    string      `xml:"FundsRecoveryId"`
	Participant        string      `xml:"Participant"`
	SituationType      string      `xml:"SituationType"`
	ContactInformation *contactXML `xml:"ContactInformation"`
	ReportDetails      *string     `xml:"ReportDetails"`
}

// recoveryActionRequest is the body of cancel, refund, block and tracking-graph; their root elements differ.
type recoveryActionRequest struct {
	XMLName         xml.Name
	Participant     string `xml:"Participant"`
	FundsRecoveryID string `xml:"FundsRecoveryId"`
}

// fundsRecoveryResponse is the response of every operation that answers one funds recovery; only the root element
// changes, and only the creation carries the graph.
type fundsRecoveryResponse struct {
	XMLName xml.Name
	header
	FundsRecovery fundsRecoveryXML  `xml:"FundsRecovery"`
	TrackingGraph *trackingGraphXML `xml:"TrackingGraph,omitempty"`
}

type getTrackingGraphResponse struct {
	XMLName xml.Name `xml:"GetTrackingGraphResponse"`
	header
	TrackingGraph trackingGraphXML `xml:"TrackingGraph"`
}

type listFundsRecoveryReportsResponse struct {
	XMLName xml.Name `xml:"ListFundsRecoveryInfractionReportsResponse"`
	header
	InfractionReports []infractionReportXML `xml:"InfractionReports>InfractionReport"`
}

type listFundsRecoveryRefundsResponse struct {
	XMLName xml.Name `xml:"ListFundsRecoveryRefundsResponse"`
	header
	Refunds []refundXML `xml:"Refunds>Refund"`
}

type eventNotificationXML struct {
	ID          int64   `xml:"Id"`
	Event       string  `xml:"Event"`
	EntityType  string  `xml:"EntityType"`
	EntityID    string  `xml:"EntityId"`
	Timestamp   isoTime `xml:"Timestamp"`
	Participant string  `xml:"Participant"`
}

type listEventNotificationsResponse struct {
	XMLName xml.Name `xml:"ListEventNotificationsResponse"`
	header
	HasMoreElements    bool                   `xml:"HasMoreElements"`
	NextCursor         string                 `xml:"NextCursor,omitempty"`
	EventNotifications []eventNotificationXML `xml:"EventNotifications>EventNotification"`
}
