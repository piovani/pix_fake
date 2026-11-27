package dict

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Funds recovery (recuperação de valores): the payer's participant asks the DICT to follow the money of a fraud from
// the reported transaction (the root) through the accounts it went on to, and to block and send back what it can.
//
// The spec keeps only the AUTOMATED flow: the DICT draws the tracking graph and opens an infraction report on each
// transaction of it when the funds recovery is created; the INTERACTIVE flow's operations (draw the graph again, block)
// are deprecated. Once every report is analysed the funds recovery is ANALYSED, the creator asks for the refunds, and
// it is COMPLETED when every refund is closed.

type RecoveryStatus string

const (
	RecoveryCreated          RecoveryStatus = "CREATED"
	RecoveryTracked          RecoveryStatus = "TRACKED"
	RecoveryAwaitingAnalysis RecoveryStatus = "AWAITING_ANALYSIS"
	RecoveryAnalysed         RecoveryStatus = "ANALYSED"
	RecoveryRefunding        RecoveryStatus = "REFUNDING"
	RecoveryCompleted        RecoveryStatus = "COMPLETED"
	RecoveryCancelled        RecoveryStatus = "CANCELLED"
)

// FlowAutomated is the only flow the spec still accepts.
const FlowAutomated = "AUTOMATED"

// GraphParameters bound the tracking graph.
type GraphParameters struct {
	MinAmount       int64         // centavos: transactions below it are left out
	MaxTransactions int           // of the whole graph
	HopWindow       time.Duration // between the money coming into an account and going out of it
	MaxHops         int           // depth of the graph, the root being depth 1
}

// GraphPerson is an owner of an account of the graph, under a pseudo id: the graph never shows tax ids.
type GraphPerson struct {
	ID    int
	Legal bool
}

// GraphAccount is an account of the graph. The DICT only knows a transaction's participants and tax ids, so an account
// is a user at a participant.
type GraphAccount struct {
	ID          int
	OwnerID     int
	Participant string
}

type GraphTransaction struct {
	ID                string
	DebtorAccountID   int
	CreditorAccountID int
	Amount            int64 // centavos
	Refundable        int64 // centavos: what is left of it to be refunded
	SettlementTime    time.Time
	Depth             int
}

type TrackingGraph struct {
	Created      time.Time
	Parameters   GraphParameters
	Persons      []GraphPerson
	Accounts     []GraphAccount
	Transactions []GraphTransaction // breadth first from the root
}

type FundsRecovery struct {
	ID                string
	RootTransactionID string
	Situation         SituationType
	ReportDetails     string
	Contact           *Contact
	Status            RecoveryStatus
	Reporter          string // the participant that created it, the payer's of the root
	Graph             TrackingGraph
	Created           time.Time
	Modified          time.Time
}

// The bounds of the graph parameters. The spec gives none; these are the mock's.
var (
	DefaultGraphParameters = GraphParameters{MinAmount: 0, MaxTransactions: 500, HopWindow: 24 * time.Hour, MaxHops: 5}
	minGraphParameters     = GraphParameters{MinAmount: 0, MaxTransactions: 1, HopWindow: time.Hour, MaxHops: 1}
	maxGraphParameters     = GraphParameters{MinAmount: 100_000_000, MaxTransactions: 1000, HopWindow: 7 * 24 * time.Hour, MaxHops: 10}
)

// GraphInput is what a request gives of the graph parameters: nil for the default.
type GraphInput struct {
	MinAmount       *int64
	MaxTransactions *int
	HopWindow       *time.Duration
	MaxHops         *int
}

// parameters fills the defaults and checks the bounds, each value out of them being a violation with its range.
func (in GraphInput) parameters() (GraphParameters, violations) {
	p := DefaultGraphParameters
	var v violations
	out := func(property, value, lo, hi string) {
		v = append(v, Violation{Property: property, Value: value, Min: lo, Max: hi, Reason: "Value must be between " + lo + " and " + hi})
	}
	if in.MinAmount != nil {
		if p.MinAmount = *in.MinAmount; p.MinAmount < minGraphParameters.MinAmount || p.MinAmount > maxGraphParameters.MinAmount {
			out("MinTransactionAmount", FormatAmount(p.MinAmount), FormatAmount(minGraphParameters.MinAmount), FormatAmount(maxGraphParameters.MinAmount))
		}
	}
	if in.MaxTransactions != nil {
		if p.MaxTransactions = *in.MaxTransactions; p.MaxTransactions < minGraphParameters.MaxTransactions || p.MaxTransactions > maxGraphParameters.MaxTransactions {
			out("MaxTransactions", strconv.Itoa(p.MaxTransactions), strconv.Itoa(minGraphParameters.MaxTransactions), strconv.Itoa(maxGraphParameters.MaxTransactions))
		}
	}
	if in.HopWindow != nil {
		if p.HopWindow = *in.HopWindow; p.HopWindow < minGraphParameters.HopWindow || p.HopWindow > maxGraphParameters.HopWindow {
			out("HopWindow", FormatDuration(p.HopWindow), FormatDuration(minGraphParameters.HopWindow), FormatDuration(maxGraphParameters.HopWindow))
		}
	}
	if in.MaxHops != nil {
		if p.MaxHops = *in.MaxHops; p.MaxHops < minGraphParameters.MaxHops || p.MaxHops > maxGraphParameters.MaxHops {
			out("MaxHops", strconv.Itoa(p.MaxHops), strconv.Itoa(minGraphParameters.MaxHops), strconv.Itoa(maxGraphParameters.MaxHops))
		}
	}
	return p, v
}

var durationRe = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// ParseDuration reads an ISO 8601 duration of days, hours, minutes and seconds ("PT24H", "P1DT12H").
func ParseDuration(s string) (time.Duration, error) {
	m := durationRe.FindStringSubmatch(s)
	if m == nil || s == "P" || s[len(s)-1] == 'T' {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	var d time.Duration
	for i, unit := range []time.Duration{24 * time.Hour, time.Hour, time.Minute, time.Second} {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(m[i+1], 10, 64)
		if err != nil || n > int64(maxGraphParameters.HopWindow/unit)*1000 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		d += time.Duration(n) * unit
	}
	return d, nil
}

// FormatDuration writes a duration in ISO 8601 with hours, minutes and seconds, the way the spec writes the hop window
// ("PT24H").
func FormatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d <= 0 {
		return "PT0S"
	}
	out := "PT"
	h, m, sec := d/time.Hour, d%time.Hour/time.Minute, d%time.Minute/time.Second
	if h > 0 {
		out += strconv.FormatInt(int64(h), 10) + "H"
	}
	if m > 0 {
		out += strconv.FormatInt(int64(m), 10) + "M"
	}
	if sec > 0 {
		out += strconv.FormatInt(int64(sec), 10) + "S"
	}
	return out
}

func recoveryNotFound() error { return newError(KindNotFound, "Funds recovery not found") }

func recoveryInvalid(v violations) error {
	return invalid(KindRecoveryInvalid, "FundsRecovery has invalid fields", v)
}

func operationInvalid(f FundsRecovery, what string) error {
	return newError(KindRecoveryOperation, fmt.Sprintf("The funds recovery is %s: %s", f.Status, what))
}

// sees reports whether the caller may read a funds recovery: its creator, and the counterparties of its reports.
func sees(f FundsRecovery, reports []InfractionReport, caller string) bool {
	if caller == f.Reporter {
		return true
	}
	for _, r := range reports {
		if caller == r.Counterparty {
			return true
		}
	}
	return false
}

// counterparties are the participants told about a funds recovery: those of its reports, other than its creator.
func counterparties(f FundsRecovery, reports []InfractionReport) []string {
	var out []string
	seen := map[string]bool{f.Reporter: true}
	for _, r := range reports {
		if !seen[r.Counterparty] {
			seen[r.Counterparty] = true
			out = append(out, r.Counterparty)
		}
	}
	return out
}

// notify stores one event notification for each participant.
func (s *Service) notify(ctx context.Context, tx MEDTx, e EventType, f FundsRecovery, participants ...string) error {
	for _, p := range participants {
		if err := tx.AddEvent(ctx, Event{Type: e, EntityID: f.ID, Participant: p, Time: s.stamp()}); err != nil {
			return err
		}
	}
	return nil
}

type CreateRecoveryInput struct {
	Participant       string
	FlowType          string // empty or AUTOMATED
	RootTransactionID string
	Situation         SituationType
	ReportDetails     string
	Contact           *Contact
	Graph             GraphInput
}

// hop is a transaction reached by the tracking, with what can be blocked of it: no more than what came into the
// account it left from, shared among the transactions out of that account in the order they were made.
type hop struct {
	t          Transaction
	depth      int
	refundable int64
	block      int64
}

// track draws the graph breadth first from the root: out of each account, the transactions made within the hop window
// after the money came in, from MinAmount up, until MaxHops deep or MaxTransactions in all.
func track(ctx context.Context, tx MEDTx, root Transaction, p GraphParameters) ([]hop, error) {
	refundableOf := func(t Transaction) (int64, error) {
		refunds, err := tx.RefundsOf(ctx, t.ID)
		return refundable(t, refunds), err
	}
	left, err := refundableOf(root)
	if err != nil {
		return nil, err
	}
	hops := []hop{{t: root, depth: 1, refundable: left, block: left}}
	seen := map[string]bool{root.ID: true}
	for i := 0; i < len(hops) && len(hops) < p.MaxTransactions; i++ {
		h := hops[i]
		if h.depth == p.MaxHops {
			continue
		}
		outs, err := tx.TransactionsFrom(ctx, h.t.CreditorParticipant, h.t.CreditorTaxID)
		if err != nil {
			return nil, err
		}
		available := h.block
		for _, o := range outs {
			if len(hops) == p.MaxTransactions {
				break
			}
			gap := o.SettlementTime.Sub(h.t.SettlementTime)
			if seen[o.ID] || o.Amount < p.MinAmount || gap <= 0 || gap > p.HopWindow {
				continue
			}
			seen[o.ID] = true
			r, err := refundableOf(o)
			if err != nil {
				return nil, err
			}
			b := min(r, available)
			available -= b
			hops = append(hops, hop{t: o, depth: h.depth + 1, refundable: r, block: b})
		}
	}
	return hops, nil
}

// graphOf lays the hops out as the spec's graph, with pseudo ids for the users and their accounts.
func graphOf(hops []hop, p GraphParameters, now time.Time) TrackingGraph {
	g := TrackingGraph{Created: now, Parameters: p}
	persons, accounts := map[string]int{}, map[[2]string]int{}
	account := func(participant, taxID string) int {
		if id, ok := accounts[[2]string{participant, taxID}]; ok {
			return id
		}
		owner, ok := persons[taxID]
		if !ok {
			owner = len(persons) + 1
			persons[taxID] = owner
			g.Persons = append(g.Persons, GraphPerson{ID: owner, Legal: len(taxID) == 14})
		}
		id := len(accounts) + 1
		accounts[[2]string{participant, taxID}] = id
		g.Accounts = append(g.Accounts, GraphAccount{ID: id, OwnerID: owner, Participant: participant})
		return id
	}
	for _, h := range hops {
		g.Transactions = append(g.Transactions, GraphTransaction{
			ID: h.t.ID, DebtorAccountID: account(h.t.DebtorParticipant, h.t.DebtorTaxID),
			CreditorAccountID: account(h.t.CreditorParticipant, h.t.CreditorTaxID), Amount: h.t.Amount, Refundable: h.refundable,
			SettlementTime: h.t.SettlementTime, Depth: h.depth,
		})
	}
	return g
}

// inProgress reports whether a transaction has a REFUND_REQUEST report still being analysed.
func inProgress(reports []InfractionReport) bool {
	for _, r := range reports {
		if r.Reason == ReasonRefundRequest && (r.Status == ReportOpen || r.Status == ReportAcknowledged) {
			return true
		}
	}
	return false
}

// CreateFundsRecovery opens a funds recovery on a transaction the caller's user paid. It draws the tracking graph and
// opens an infraction report, with the amount to block, on each transaction of it that has something to block and no
// report in progress, all at once: the funds recovery is AWAITING_ANALYSIS when this returns. There is one funds
// recovery per root transaction, ever.
func (s *Service) CreateFundsRecovery(ctx context.Context, caller string, in CreateRecoveryInput) (FundsRecovery, error) {
	var v violations
	validateRegexp("participant", in.Participant, ispbRe, &v)
	validateRegexp("fundsRecovery.rootTransactionId", in.RootTransactionID, refundTransactionRe, &v)
	if in.FlowType != "" && in.FlowType != FlowAutomated {
		v.add("fundsRecovery.flowType", in.FlowType, "Invalid flow type, valid values: AUTOMATED")
	}
	validateSituation("fundsRecovery", in.Situation, in.ReportDetails, &v)
	if in.Contact == nil {
		v.add("fundsRecovery.contactInformation", "", "Value is required")
	}
	validateContact("fundsRecovery.contactInformation", in.Contact, &v)
	if len(v) > 0 {
		return FundsRecovery{}, recoveryInvalid(v)
	}
	params, pv := in.Graph.parameters()
	if len(pv) > 0 {
		return FundsRecovery{}, invalid(KindRecoveryGraphInvalid, "TrackingGraphParameters has values out of range", pv)
	}
	if in.Participant != caller {
		return FundsRecovery{}, newError(KindForbidden, "Participant is not allowed to create funds recoveries for another participant")
	}

	var out FundsRecovery
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		root, err := tx.Transaction(ctx, in.RootTransactionID)
		if errors.Is(err, ErrNotFound) {
			return newError(KindRecoveryNotIndexed, "The root transaction was not settled in the SPI")
		}
		if err != nil {
			return err
		}
		if caller != root.DebtorParticipant {
			return newError(KindForbidden, "Only the participant of the payer can open a funds recovery on this transaction")
		}
		if _, err := tx.FundsRecoveryOf(ctx, root.ID); err == nil {
			return newError(KindRecoveryExists, "There is already a funds recovery on this root transaction")
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if root.Amount < params.MinAmount {
			return newError(KindRecoveryNotIndexed, "The root transaction is below MinTransactionAmount")
		}
		reports, err := tx.ReportsOf(ctx, root.ID)
		if err != nil {
			return err
		}
		if inProgress(reports) {
			return newError(KindRecoveryNotEligible, "There is already an infraction report in progress for the root transaction")
		}
		hops, err := track(ctx, tx, root, params)
		if err != nil {
			return err
		}
		if hops[0].block == 0 {
			return newError(KindRecoveryNotEligible, "Nothing is left to be refunded of the root transaction")
		}
		now := s.stamp()
		out = FundsRecovery{
			ID: s.newID(), RootTransactionID: root.ID, Situation: in.Situation, ReportDetails: in.ReportDetails, Contact: in.Contact,
			Status: RecoveryAwaitingAnalysis, Reporter: caller, Graph: graphOf(hops, params, now), Created: now, Modified: now,
		}
		if err := tx.SaveFundsRecovery(ctx, out); err != nil {
			return err
		}
		for _, h := range hops {
			if h.block == 0 {
				continue
			}
			if h.depth > 1 {
				reports, err := tx.ReportsOf(ctx, h.t.ID)
				if err != nil {
					return err
				}
				if inProgress(reports) {
					continue
				}
			}
			if err := tx.SaveReport(ctx, InfractionReport{
				ID: s.newID(), TransactionID: h.t.ID, Reason: ReasonRefundRequest, Situation: in.Situation, ReportDetails: in.ReportDetails,
				Contact: in.Contact, Status: ReportOpen, Reporter: h.t.DebtorParticipant, Counterparty: h.t.CreditorParticipant,
				Amount: h.block, CreditorTaxID: h.t.CreditorTaxID, CreditorKey: h.t.CreditorKey, FundsRecoveryID: out.ID,
				Depth: h.depth, Created: now, Modified: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// readRecovery runs fn on a funds recovery the caller can see, with its reports, read without locking.
func (s *Service) readRecovery(ctx context.Context, caller, id string, fn func(tx MEDTx, f FundsRecovery, reports []InfractionReport) error) error {
	return s.repo.InTx(ctx, func(tx MEDTx) error {
		f, err := tx.FundsRecovery(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return recoveryNotFound()
		}
		if err != nil {
			return err
		}
		reports, err := tx.ReportsOfRecovery(ctx, id, false)
		if err != nil {
			return err
		}
		if !sees(f, reports, caller) {
			return recoveryNotFound()
		}
		return fn(tx, f, reports)
	})
}

// GetFundsRecovery returns a funds recovery the caller created or has reports of.
func (s *Service) GetFundsRecovery(ctx context.Context, caller, id string) (FundsRecovery, error) {
	var out FundsRecovery
	err := s.readRecovery(ctx, caller, id, func(_ MEDTx, f FundsRecovery, _ []InfractionReport) error {
		out = f
		return nil
	})
	return out, err
}

// mutateRecovery runs fn on a funds recovery of the caller, inside one transaction, and stores what fn left. Its
// reports and refunds are locked before it, the order every other writer of them follows.
func (s *Service) mutateRecovery(ctx context.Context, caller, participant, id string,
	fn func(tx MEDTx, f *FundsRecovery, reports []InfractionReport, refunds []Refund) (changed bool, err error)) (FundsRecovery, error) {
	if participant != caller {
		return FundsRecovery{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	var out FundsRecovery
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		reports, err := tx.ReportsOfRecovery(ctx, id, true)
		if err != nil {
			return err
		}
		refunds, err := tx.RefundsOfRecovery(ctx, id, true)
		if err != nil {
			return err
		}
		f, err := tx.FundsRecovery(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return recoveryNotFound()
		}
		if err != nil {
			return err
		}
		if !sees(f, reports, caller) {
			return recoveryNotFound()
		}
		if caller != f.Reporter {
			return newError(KindForbidden, "Only the participant that created the funds recovery can change it")
		}
		changed, err := fn(tx, &f, reports, refunds)
		if err != nil {
			return err
		}
		if changed {
			f.Modified = s.stamp()
			if err := tx.SaveFundsRecovery(ctx, f); err != nil {
				return err
			}
		}
		out = f
		return nil
	})
	return out, err
}

type UpdateRecoveryInput struct {
	Participant   string
	Situation     SituationType // empty: unchanged
	ReportDetails *string
	Contact       *Contact
}

// UpdateFundsRecovery changes the situation, details and contact of a funds recovery until it is analysed, and of its
// reports still being analysed. The counterparties are told.
func (s *Service) UpdateFundsRecovery(ctx context.Context, caller, id string, in UpdateRecoveryInput) (FundsRecovery, error) {
	return s.mutateRecovery(ctx, caller, in.Participant, id, func(tx MEDTx, f *FundsRecovery, reports []InfractionReport, _ []Refund) (bool, error) {
		switch f.Status {
		case RecoveryCreated, RecoveryTracked, RecoveryAwaitingAnalysis:
		default:
			return false, operationInvalid(*f, "it can only be updated until it is analysed")
		}
		if in.Situation != "" {
			f.Situation = in.Situation
		}
		if in.ReportDetails != nil {
			f.ReportDetails = *in.ReportDetails
		}
		if in.Contact != nil {
			f.Contact = in.Contact
		}
		var v violations
		validateSituation("fundsRecovery", f.Situation, f.ReportDetails, &v)
		validateContact("contactInformation", in.Contact, &v)
		if len(v) > 0 {
			return false, recoveryInvalid(v)
		}
		now := s.stamp()
		for _, r := range reports {
			if r.Status == ReportOpen || r.Status == ReportAcknowledged {
				r.Situation, r.ReportDetails, r.Contact, r.Modified = f.Situation, f.ReportDetails, f.Contact, now
				if err := tx.SaveReport(ctx, r); err != nil {
					return false, err
				}
			}
		}
		return true, s.notify(ctx, tx, EventRecoveryUpdated, *f, counterparties(*f, reports)...)
	})
}

// CancelFundsRecovery is the creator giving up, at any moment before it is completed: every report of it is cancelled
// (with the fraud markers they made), and every refund still open. Repeating it is harmless.
func (s *Service) CancelFundsRecovery(ctx context.Context, caller, participant, id string) (FundsRecovery, error) {
	return s.mutateRecovery(ctx, caller, participant, id, func(tx MEDTx, f *FundsRecovery, reports []InfractionReport, refunds []Refund) (bool, error) {
		switch f.Status {
		case RecoveryCancelled:
			return false, nil
		case RecoveryCompleted:
			return false, operationInvalid(*f, "it can no longer be cancelled")
		}
		now := s.stamp()
		for _, r := range reports {
			if r.Status == ReportCancelled {
				continue
			}
			if err := s.cancelReport(ctx, tx, &r); err != nil {
				return false, err
			}
			r.Modified = now
			if err := tx.SaveReport(ctx, r); err != nil {
				return false, err
			}
		}
		for _, r := range refunds {
			if r.Status == RefundOpen {
				r.Status, r.Modified = RefundCancelled, now
				if err := tx.SaveRefund(ctx, r); err != nil {
					return false, err
				}
			}
		}
		f.Status = RecoveryCancelled
		return true, s.notify(ctx, tx, EventRecoveryCancelled, *f, counterparties(*f, reports)...)
	})
}

// RefundFundsRecovery asks for the money back, once every report is analysed: a FRAUD refund on each transaction whose
// report was closed with AGREED, of what was blocked and is still refundable. With none, the funds recovery is
// completed at once.
func (s *Service) RefundFundsRecovery(ctx context.Context, caller, participant, id string) (FundsRecovery, error) {
	return s.mutateRecovery(ctx, caller, participant, id, func(tx MEDTx, f *FundsRecovery, reports []InfractionReport, _ []Refund) (bool, error) {
		if f.Status != RecoveryAnalysed {
			return false, operationInvalid(*f, "refunds can only be asked for when it is ANALYSED")
		}
		now := s.stamp()
		asked := 0
		for _, r := range reports {
			if r.Status != ReportClosed || r.AnalysisResult != AnalysisAgreed {
				continue
			}
			t, err := tx.Transaction(ctx, r.TransactionID)
			if err != nil {
				return false, err
			}
			refunds, err := tx.RefundsOf(ctx, t.ID)
			if err != nil {
				return false, err
			}
			open := false
			for _, x := range refunds {
				open = open || x.Status == RefundOpen
			}
			amount := min(r.Amount, refundable(t, refunds))
			if open || amount <= 0 {
				continue
			}
			if err := tx.SaveRefund(ctx, Refund{
				ID: s.newID(), TransactionID: t.ID, Reason: RefundFraud, Amount: amount, Status: RefundOpen, Requesting: r.Reporter,
				Contested: r.Counterparty, InfractionReportID: r.ID, FundsRecoveryID: f.ID, Created: now, Modified: now,
			}); err != nil {
				return false, err
			}
			asked++
		}
		if asked == 0 {
			f.Status = RecoveryCompleted
			return true, s.notify(ctx, tx, EventRecoveryCompleted, *f, f.Reporter)
		}
		f.Status = RecoveryRefunding
		return true, nil
	})
}

// GetTrackingGraph returns the graph of a funds recovery the caller can see.
func (s *Service) GetTrackingGraph(ctx context.Context, caller, id string) (FundsRecovery, error) {
	return s.GetFundsRecovery(ctx, caller, id)
}

// creatorOnly runs fn on a funds recovery of the caller. Its lists are the creator's only, and the spec answers
// anyone else with 403, a funds recovery that does not exist included.
func (s *Service) creatorOnly(ctx context.Context, caller, id string, fn func(tx MEDTx, f FundsRecovery) error) error {
	return s.repo.InTx(ctx, func(tx MEDTx) error {
		f, err := tx.FundsRecovery(ctx, id)
		if errors.Is(err, ErrNotFound) || err == nil && f.Reporter != caller {
			return newError(KindForbidden, "Only the participant that created the funds recovery can list its records")
		}
		if err != nil {
			return err
		}
		return fn(tx, f)
	})
}

// FundsRecoveryReports lists the infraction reports a funds recovery opened.
func (s *Service) FundsRecoveryReports(ctx context.Context, caller, id string) ([]InfractionReport, error) {
	var out []InfractionReport
	err := s.creatorOnly(ctx, caller, id, func(tx MEDTx, _ FundsRecovery) error {
		var err error
		out, err = tx.ReportsOfRecovery(ctx, id, false)
		return err
	})
	return out, err
}

// FundsRecoveryRefunds lists the refunds a funds recovery asked for, once it did.
func (s *Service) FundsRecoveryRefunds(ctx context.Context, caller, id string) ([]Refund, error) {
	var out []Refund
	err := s.creatorOnly(ctx, caller, id, func(tx MEDTx, f FundsRecovery) error {
		refunds, err := tx.RefundsOfRecovery(ctx, id, false)
		if err != nil {
			return err
		}
		if len(refunds) == 0 && f.Status != RecoveryRefunding && f.Status != RecoveryCompleted {
			return operationInvalid(f, "no refund was asked for yet")
		}
		out = refunds
		return nil
	})
	return out, err
}

// DeprecatedOperation answers the operations of the INTERACTIVE flow, which the spec deprecated: drawing the graph
// again and blocking.
func (s *Service) DeprecatedOperation() error {
	return newError(KindDeprecated, "This operation belongs to the INTERACTIVE flow, which is deprecated: the DICT tracks and blocks when the funds recovery is created")
}

// reportAnalysed moves a funds recovery to ANALYSED when the report just closed was the last one being analysed.
func (s *Service) reportAnalysed(ctx context.Context, tx MEDTx, closed InfractionReport) error {
	f, err := tx.FundsRecovery(ctx, closed.FundsRecoveryID)
	if err != nil || f.Status != RecoveryAwaitingAnalysis {
		return err
	}
	reports, err := tx.ReportsOfRecovery(ctx, f.ID, false)
	if err != nil {
		return err
	}
	for _, r := range reports {
		if r.ID != closed.ID && (r.Status == ReportOpen || r.Status == ReportAcknowledged) {
			return nil
		}
	}
	f.Status, f.Modified = RecoveryAnalysed, s.stamp()
	if err := tx.SaveFundsRecovery(ctx, f); err != nil {
		return err
	}
	return s.notify(ctx, tx, EventRecoveryAnalysed, f, f.Reporter)
}

// refundClosed completes a funds recovery when the refund just closed was the last one open.
func (s *Service) refundClosed(ctx context.Context, tx MEDTx, closed Refund) error {
	f, err := tx.FundsRecovery(ctx, closed.FundsRecoveryID)
	if err != nil || f.Status != RecoveryRefunding {
		return err
	}
	refunds, err := tx.RefundsOfRecovery(ctx, f.ID, false)
	if err != nil {
		return err
	}
	for _, r := range refunds {
		if r.ID != closed.ID && r.Status == RefundOpen {
			return nil
		}
	}
	f.Status, f.Modified = RecoveryCompleted, s.stamp()
	if err := tx.SaveFundsRecovery(ctx, f); err != nil {
		return err
	}
	return s.notify(ctx, tx, EventRecoveryCompleted, f, f.Reporter)
}
