package dict

import (
	"context"
	"time"
)

// MED (Mecanismo Especial de Devolução): infraction reports, fraud markers and refunds, and the transactions they are
// about. The real DICT knows every transaction settled in the SPI; the mock only knows the ones it was told about (see
// AddTransaction), so a report or refund on any other transaction is refused.

// Transaction is a payment settled in the SPI, as the DICT knows it.
type Transaction struct {
	ID                  string // EndToEndId
	DebtorParticipant   string
	DebtorTaxID         string
	CreditorParticipant string
	CreditorTaxID       string
	CreditorKey         string // the key that was paid, empty when the payment used the account directly
	Amount              int64  // centavos
	SettlementTime      time.Time
}

type InfractionReason string

const (
	// ReasonRefundRequest: the payer's participant reports a suspected fraud, to ask for the money back.
	ReasonRefundRequest InfractionReason = "REFUND_REQUEST"
	// ReasonRefundCancelled: the payee's participant reports that a refund it made has to be undone.
	ReasonRefundCancelled InfractionReason = "REFUND_CANCELLED"
)

type SituationType string

const (
	SituationScam             SituationType = "SCAM"
	SituationAccountTakeover  SituationType = "ACCOUNT_TAKEOVER"
	SituationCoercion         SituationType = "COERCION"
	SituationFraudulentAccess SituationType = "FRAUDULENT_ACCESS"
	SituationOther            SituationType = "OTHER"
	SituationUnknown          SituationType = "UNKNOWN" // only for reports of the API v1, never accepted
)

type ReportStatus string

const (
	ReportOpen         ReportStatus = "OPEN"
	ReportAcknowledged ReportStatus = "ACKNOWLEDGED"
	ReportClosed       ReportStatus = "CLOSED"
	ReportCancelled    ReportStatus = "CANCELLED"
)

type AnalysisResult string

const (
	AnalysisAgreed    AnalysisResult = "AGREED"
	AnalysisDisagreed AnalysisResult = "DISAGREED"
)

type FraudType string

const (
	FraudApplication FraudType = "APPLICATION_FRAUD"
	FraudMule        FraudType = "MULE_ACCOUNT"
	FraudScammer     FraudType = "SCAMMER_ACCOUNT"
	FraudOther       FraudType = "OTHER"
	FraudUnknown     FraudType = "UNKNOWN" // only for markers of the API v1, never accepted
)

type Contact struct {
	Email string
	Phone string
}

// InfractionReport is a report that a settled transaction is suspected of fraud, from one participant of the
// transaction (the reporter) to the other (the counterparty).
type InfractionReport struct {
	ID              string
	TransactionID   string
	Reason          InfractionReason
	Situation       SituationType
	ReportDetails   string
	Contact         *Contact
	Status          ReportStatus
	Reporter        string
	Counterparty    string
	FraudMarkerID   string // set when closed with AGREED
	AnalysisResult  AnalysisResult
	AnalysisDetails string
	FraudType       FraudType // what the counterparty gave when it closed it
	Amount          int64     // InfractionAmount, centavos: the amount of the transaction
	CreditorTaxID   string    // of the transaction, for the statistics
	CreditorKey     string
	FundsRecoveryID string // the funds recovery that created it, if any
	Depth           int    // TransactionDepth of the transaction in that funds recovery's tracking graph, 1 for the root
	Created         time.Time
	Modified        time.Time
}

type MarkerStatus string

const (
	MarkerRegistered MarkerStatus = "REGISTERED"
	MarkerCancelled  MarkerStatus = "CANCELLED"
)

// FraudMarker marks a user (and, if known, a key) as involved in a fraud. It feeds the anti-fraud statistics.
type FraudMarker struct {
	ID         string
	Registrant string // the participant that created it, or that closed the infraction report it came from
	TaxID      string
	Key        string
	FraudType  FraudType
	Status     MarkerStatus
	RequestID  string // empty when it came from an infraction report
	ReportID   string // the infraction report it came from, if any
	Reporter   string // the reporter of that infraction report
	Amount     int64  // centavos: the amount of the reported transaction, zero for a marker made directly
	Created    time.Time
	Modified   time.Time
}

type RefundReason string

const (
	RefundFraud           RefundReason = "FRAUD"
	RefundOperationalFlaw RefundReason = "OPERATIONAL_FLAW"
	RefundPixAutomatico   RefundReason = "PIX_AUTOMATICO"
	// The spec also lists "REFUND_CANCELLED (DEPRECATED)", discontinued: refused.
)

type RefundStatus string

const (
	RefundOpen      RefundStatus = "OPEN"
	RefundClosed    RefundStatus = "CLOSED"
	RefundCancelled RefundStatus = "CANCELLED"
)

type RefundResult string

const (
	RefundTotallyAccepted   RefundResult = "TOTALLY_ACCEPTED"
	RefundPartiallyAccepted RefundResult = "PARTIALLY_ACCEPTED"
	RefundRejected          RefundResult = "REJECTED"
)

type RejectionReason string

const (
	RejectNoBalance            RejectionReason = "NO_BALANCE"
	RejectAccountClosure       RejectionReason = "ACCOUNT_CLOSURE"
	RejectInvalidRequest       RejectionReason = "INVALID_REQUEST"
	RejectOther                RejectionReason = "OTHER"
	RejectParticipantExclusion RejectionReason = "PARTICIPANT_EXCLUSION" // internal to the DICT, never accepted
)

// Refund is a request, from the payer's participant (requesting) to the payee's (contested), to send money of a
// transaction back. The money itself moves in the SPI, as a return (pacs.004), whose id closes the refund.
type Refund struct {
	ID                  string
	TransactionID       string
	Reason              RefundReason
	Amount              int64 // centavos
	Details             string
	Status              RefundStatus
	Requesting          string
	Contested           string
	InfractionReportID  string // for FRAUD
	AnalysisResult      RefundResult
	AnalysisDetails     string
	RejectionReason     RejectionReason
	RefundTransactionID string
	FundsRecoveryID     string // the funds recovery that created it, if any
	Created             time.Time
	Modified            time.Time
}

// MEDTx reads and writes the MED records inside one transaction of MEDStore.InTx. What it reads is locked until the
// transaction ends, so two requests on the same record or transaction go one after the other.
type MEDTx interface {
	Transaction(ctx context.Context, id string) (Transaction, error)
	Report(ctx context.Context, id string) (InfractionReport, error)
	ReportsOf(ctx context.Context, transactionID string) ([]InfractionReport, error)
	SaveReport(ctx context.Context, r InfractionReport) error // creates or replaces
	Marker(ctx context.Context, id string) (FraudMarker, error)
	MarkerByRequestID(ctx context.Context, participant, requestID string) (FraudMarker, error)
	SaveMarker(ctx context.Context, m FraudMarker) error
	Refund(ctx context.Context, id string) (Refund, error)
	RefundsOf(ctx context.Context, transactionID string) ([]Refund, error)
	SaveRefund(ctx context.Context, r Refund) error

	// TransactionsFrom returns the transactions paid by a user at a participant, oldest settlement first.
	TransactionsFrom(ctx context.Context, participant, taxID string) ([]Transaction, error)
	FundsRecovery(ctx context.Context, id string) (FundsRecovery, error)
	// FundsRecoveryOf returns the funds recovery whose root is the transaction, cancelled or not.
	FundsRecoveryOf(ctx context.Context, rootTransactionID string) (FundsRecovery, error)
	SaveFundsRecovery(ctx context.Context, f FundsRecovery) error
	// ReportsOfRecovery and RefundsOfRecovery return the records a funds recovery created, oldest first. With lock
	// they are locked like any other read; without, they are only read, for a function that already holds the funds
	// recovery: whatever changes their status in a way it cares about holds it too.
	ReportsOfRecovery(ctx context.Context, recoveryID string, lock bool) ([]InfractionReport, error)
	RefundsOfRecovery(ctx context.Context, recoveryID string, lock bool) ([]Refund, error)
	// AddEvent stores an event notification, numbered after every one before it.
	AddEvent(ctx context.Context, e Event) error
}

// ReportQuery selects infraction reports: each field that is not empty must match.
type ReportQuery struct {
	Participant   string // reporter or counterparty
	CreditorTaxID string
	CreditorKey   string
}

// MarkerQuery selects fraud markers: each field that is not empty must match.
type MarkerQuery struct {
	TaxID string
	Key   string
}

// TransactionQuery selects transactions: each field that is not empty must match.
type TransactionQuery struct {
	CreditorTaxID string
	CreditorKey   string
}

// MEDStore keeps the MED records. Reads that are not in InTx see what is committed; lists are oldest change first.
type MEDStore interface {
	InTx(ctx context.Context, fn func(MEDTx) error) error
	// AddTransaction stores a transaction, or replaces the one with the same id.
	AddTransaction(ctx context.Context, t Transaction) error
	ListTransactions(ctx context.Context, q TransactionQuery) ([]Transaction, error)
	ListReports(ctx context.Context, q ReportQuery) ([]InfractionReport, error)
	ListMarkers(ctx context.Context, q MarkerQuery) ([]FraudMarker, error)
	// ListRefunds returns the refunds in which the participant is requesting or contested.
	ListRefunds(ctx context.Context, participant string) ([]Refund, error)
	// EntriesOf returns the entries whose owner has the tax id.
	EntriesOf(ctx context.Context, taxID string) ([]Entry, error)
	// ListEvents returns the event notifications of a participant numbered after afterID, in order, at most limit.
	ListEvents(ctx context.Context, participant string, afterID int64, since time.Time, limit int) ([]Event, error)
}
