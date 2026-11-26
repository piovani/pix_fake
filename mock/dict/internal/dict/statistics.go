package dict

import (
	"context"
	"errors"
	"time"
)

// brt is the time zone of the windows of the statistics (Brasília, UTC-3, no daylight saving time since 2019).
var brt = time.FixedZone("BRT", -3*60*60)

// Windowed is a counter over the three windows of the spec: d90 the last 89 days and today, m12 and m60 the last 12
// and 60 months before the current one.
type Windowed struct{ D90, M12, M60 int64 }

type windows struct {
	d90, m12, m60 [2]time.Time // [start, end)
}

func windowsAt(now time.Time) windows {
	local := now.In(brt)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, brt)
	month := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, brt)
	end := now.Add(time.Nanosecond) // d90 includes now
	return windows{
		d90: [2]time.Time{today.AddDate(0, 0, -89), end},
		m12: [2]time.Time{month.AddDate(0, -12, 0), month},
		m60: [2]time.Time{month.AddDate(0, -60, 0), month},
	}
}

func in(t time.Time, w [2]time.Time) bool { return !t.Before(w[0]) && t.Before(w[1]) }

// add counts n at t in each window that holds t.
func (c *Windowed) add(w windows, t time.Time, n int64) {
	if in(t, w.d90) {
		c.D90 += n
	}
	if in(t, w.m12) {
		c.M12 += n
	}
	if in(t, w.m60) {
		c.M60 += n
	}
}

// distinct counts each value once per window.
type distinct struct {
	w             windows
	d90, m12, m60 map[string]bool
}

func newDistinct(w windows) *distinct {
	return &distinct{w: w, d90: map[string]bool{}, m12: map[string]bool{}, m60: map[string]bool{}}
}

func (d *distinct) add(t time.Time, v string) {
	if in(t, d.w.d90) {
		d.d90[v] = true
	}
	if in(t, d.w.m12) {
		d.m12[v] = true
	}
	if in(t, d.w.m60) {
		d.m60[v] = true
	}
}

func (d *distinct) counts() Windowed {
	return Windowed{int64(len(d.d90)), int64(len(d.m12)), int64(len(d.m60))}
}

// Statistics are the anti-fraud counters of a user or a key.
type Statistics struct {
	Watermark                    time.Time
	Settlements                  Windowed // as payee, in the SPI
	ApplicationFrauds            Windowed
	MuleAccounts                 Windowed
	ScammerAccounts              Windowed
	OtherFrauds                  Windowed
	UnknownFrauds                Windowed
	TotalFraudTransactionAmount  Windowed // reais, truncated
	DistinctFraudReporters       Windowed
	OpenReports                  int64
	OpenReportsDistinctReporters int64
	RejectedReports              Windowed
	RegisteredAccounts           int64    // of a user: distinct accounts of its keys registered now
	DistinctAccounts             Windowed // of a key: distinct accounts it was registered to
}

// statistics counts what the DICT knows of a user (taxID) or a key. Fraud markers count while REGISTERED: cancelling
// one takes it out of the counters, as the spec says.
func (s *Service) statistics(ctx context.Context, taxID, key string) (Statistics, error) {
	now := s.now().UTC()
	w := windowsAt(now)
	st := Statistics{Watermark: now}
	if taxID == "" && key == "" {
		return st, nil
	}

	txs, err := s.repo.ListTransactions(ctx, TransactionQuery{CreditorTaxID: taxID, CreditorKey: key})
	if err != nil {
		return Statistics{}, err
	}
	for _, t := range txs {
		st.Settlements.add(w, t.SettlementTime, 1)
	}

	markers, err := s.repo.ListMarkers(ctx, MarkerQuery{TaxID: taxID, Key: key})
	if err != nil {
		return Statistics{}, err
	}
	reporters := newDistinct(w)
	var cents Windowed
	for _, m := range markers {
		if m.Status != MarkerRegistered {
			continue
		}
		switch m.FraudType {
		case FraudApplication:
			st.ApplicationFrauds.add(w, m.Created, 1)
		case FraudMule:
			st.MuleAccounts.add(w, m.Created, 1)
		case FraudScammer:
			st.ScammerAccounts.add(w, m.Created, 1)
		case FraudOther:
			st.OtherFrauds.add(w, m.Created, 1)
		default:
			st.UnknownFrauds.add(w, m.Created, 1)
		}
		if m.FraudType == FraudUnknown {
			continue
		}
		// a user's total leaves APPLICATION_FRAUD out (the account was opened in the victim's name); a key's does not
		if key != "" || m.FraudType != FraudApplication {
			cents.add(w, m.Created, m.Amount)
		}
		reporters.add(m.Created, m.Registrant)
	}
	st.DistinctFraudReporters = reporters.counts()
	st.TotalFraudTransactionAmount = Windowed{cents.D90 / 100, cents.M12 / 100, cents.M60 / 100} // reais, truncated

	reports, err := s.repo.ListReports(ctx, ReportQuery{CreditorTaxID: taxID, CreditorKey: key})
	if err != nil {
		return Statistics{}, err
	}
	open := map[string]bool{}
	for _, r := range reports {
		switch {
		case r.Status == ReportOpen || r.Status == ReportAcknowledged:
			st.OpenReports++
			open[r.Reporter] = true
		case r.Status == ReportClosed && r.AnalysisResult == AnalysisDisagreed:
			st.RejectedReports.add(w, r.Modified, 1)
		}
	}
	st.OpenReportsDistinctReporters = int64(len(open))

	if key != "" {
		if _, err := s.repo.Get(ctx, key); err == nil { // registered now, so registered in every window
			st.DistinctAccounts = Windowed{1, 1, 1}
		}
		return st, nil
	}
	entries, err := s.repo.EntriesOf(ctx, taxID)
	if err != nil {
		return Statistics{}, err
	}
	accounts := map[Account]bool{}
	for _, e := range entries {
		a := e.Account
		a.OpeningDate = time.Time{}
		accounts[a] = true
	}
	st.RegisteredAccounts = int64(len(accounts))
	return st, nil
}

// PersonStatistics returns the counters of a user (CPF or CNPJ).
func (s *Service) PersonStatistics(ctx context.Context, taxID string) (Statistics, error) {
	var v violations
	validateRegexp("TaxIdNumber", taxID, taxIDRe, &v)
	if len(v) > 0 {
		return Statistics{}, invalid(KindBadRequest, "Request has invalid parameters", v)
	}
	return s.statistics(ctx, taxID, "")
}

// EntryStatistics returns the counters of a key and of its owner. A key that is not registered has no owner, and
// only its own counters.
func (s *Service) EntryStatistics(ctx context.Context, key string) (owner, keyStats Statistics, err error) {
	if _, err := checkKeyFormat(key); err != nil {
		return Statistics{}, Statistics{}, err
	}
	var taxID string
	switch e, err := s.repo.Get(ctx, key); {
	case err == nil:
		taxID = e.Owner.TaxID
	case !errors.Is(err, ErrNotFound):
		return Statistics{}, Statistics{}, err
	}
	if owner, err = s.statistics(ctx, taxID, ""); err != nil {
		return Statistics{}, Statistics{}, err
	}
	keyStats, err = s.statistics(ctx, "", key)
	return owner, keyStats, err
}
