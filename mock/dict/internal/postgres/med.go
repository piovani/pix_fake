package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"pixfake/mock/dict/internal/dict"
)

// medTx is a dict.MEDTx on a PostgreSQL transaction: every read locks its rows (FOR UPDATE) until the end.
type medTx struct{ tx pgx.Tx }

func (r *Repository) InTx(ctx context.Context, fn func(dict.MEDTx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(medTx{tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- transactions ---

const transactionColumns = `id, debtor_participant, debtor_tax_id, creditor_participant, creditor_tax_id, creditor_key,
	amount_cents, settlement_time`

func scanTransaction(row pgx.Row) (dict.Transaction, error) {
	var t dict.Transaction
	err := row.Scan(&t.ID, &t.DebtorParticipant, &t.DebtorTaxID, &t.CreditorParticipant, &t.CreditorTaxID, &t.CreditorKey,
		&t.Amount, &t.SettlementTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.Transaction{}, dict.ErrNotFound
	}
	t.SettlementTime = t.SettlementTime.UTC()
	return t, err
}

func (m medTx) Transaction(ctx context.Context, id string) (dict.Transaction, error) {
	return scanTransaction(m.tx.QueryRow(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE id = $1 FOR UPDATE`, id))
}

func (r *Repository) AddTransaction(ctx context.Context, t dict.Transaction) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO transactions (`+transactionColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET debtor_participant = $2, debtor_tax_id = $3, creditor_participant = $4,
			creditor_tax_id = $5, creditor_key = $6, amount_cents = $7, settlement_time = $8`,
		t.ID, t.DebtorParticipant, t.DebtorTaxID, t.CreditorParticipant, t.CreditorTaxID, t.CreditorKey, t.Amount, t.SettlementTime)
	return err
}

func (r *Repository) ListTransactions(ctx context.Context, q dict.TransactionQuery) ([]dict.Transaction, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+transactionColumns+` FROM transactions
		WHERE ($1 = '' OR creditor_tax_id = $1) AND ($2 = '' OR creditor_key = $2) ORDER BY settlement_time, id`,
		q.CreditorTaxID, q.CreditorKey)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.Transaction, error) { return scanTransaction(row) })
}

// --- infraction reports ---

const reportColumns = `id::text, transaction_id, reason, situation_type, report_details, contact_email, contact_phone, status,
	reporter, counterparty, COALESCE(fraud_marker_id::text, ''), analysis_result, analysis_details, fraud_type,
	amount_cents, creditor_tax_id, creditor_key, created, modified`

func scanReport(row pgx.Row) (dict.InfractionReport, error) {
	var (
		r                         dict.InfractionReport
		reason, situation, status string
		result, fraudType         string
		email, phone              *string
	)
	err := row.Scan(&r.ID, &r.TransactionID, &reason, &situation, &r.ReportDetails, &email, &phone, &status,
		&r.Reporter, &r.Counterparty, &r.FraudMarkerID, &result, &r.AnalysisDetails, &fraudType,
		&r.Amount, &r.CreditorTaxID, &r.CreditorKey, &r.Created, &r.Modified)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.InfractionReport{}, dict.ErrNotFound
	}
	if err != nil {
		return dict.InfractionReport{}, err
	}
	r.Reason, r.Situation, r.Status = dict.InfractionReason(reason), dict.SituationType(situation), dict.ReportStatus(status)
	r.AnalysisResult, r.FraudType = dict.AnalysisResult(result), dict.FraudType(fraudType)
	if email != nil {
		r.Contact = &dict.Contact{Email: *email, Phone: deref(phone)}
	}
	r.Created, r.Modified = r.Created.UTC(), r.Modified.UTC()
	return r, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func collectReports(rows pgx.Rows, err error) ([]dict.InfractionReport, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.InfractionReport, error) { return scanReport(row) })
}

func (m medTx) Report(ctx context.Context, id string) (dict.InfractionReport, error) {
	if !isUUID(id) {
		return dict.InfractionReport{}, dict.ErrNotFound
	}
	return scanReport(m.tx.QueryRow(ctx, `SELECT `+reportColumns+` FROM infraction_reports WHERE id = $1 FOR UPDATE`, id))
}

func (m medTx) ReportsOf(ctx context.Context, transactionID string) ([]dict.InfractionReport, error) {
	return collectReports(m.tx.Query(ctx, `SELECT `+reportColumns+` FROM infraction_reports WHERE transaction_id = $1
		ORDER BY modified, id FOR UPDATE`, transactionID))
}

func (m medTx) SaveReport(ctx context.Context, r dict.InfractionReport) error {
	var email, phone *string
	if r.Contact != nil {
		email, phone = &r.Contact.Email, &r.Contact.Phone
	}
	var marker *string
	if r.FraudMarkerID != "" {
		marker = &r.FraudMarkerID
	}
	_, err := m.tx.Exec(ctx, `
		INSERT INTO infraction_reports (id, transaction_id, reason, situation_type, report_details, contact_email, contact_phone,
			status, reporter, counterparty, fraud_marker_id, analysis_result, analysis_details, fraud_type, amount_cents,
			creditor_tax_id, creditor_key, created, modified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (id) DO UPDATE SET situation_type = $4, report_details = $5, contact_email = $6, contact_phone = $7,
			status = $8, fraud_marker_id = $11, analysis_result = $12, analysis_details = $13, fraud_type = $14, modified = $19`,
		r.ID, r.TransactionID, r.Reason, r.Situation, r.ReportDetails, email, phone, r.Status, r.Reporter, r.Counterparty,
		marker, r.AnalysisResult, r.AnalysisDetails, r.FraudType, r.Amount, r.CreditorTaxID, r.CreditorKey, r.Created, r.Modified)
	return err
}

func (r *Repository) ListReports(ctx context.Context, q dict.ReportQuery) ([]dict.InfractionReport, error) {
	return collectReports(r.pool.Query(ctx, `SELECT `+reportColumns+` FROM infraction_reports
		WHERE ($1 = '' OR reporter = $1 OR counterparty = $1) AND ($2 = '' OR creditor_tax_id = $2) AND ($3 = '' OR creditor_key = $3)
		ORDER BY modified, id`, q.Participant, q.CreditorTaxID, q.CreditorKey))
}

// --- fraud markers ---

const markerColumns = `id::text, registrant, tax_id, key, fraud_type, status, COALESCE(request_id::text, ''),
	COALESCE(report_id::text, ''), reporter, amount_cents, created, modified`

func scanMarker(row pgx.Row) (dict.FraudMarker, error) {
	var m dict.FraudMarker
	var fraudType, status string
	err := row.Scan(&m.ID, &m.Registrant, &m.TaxID, &m.Key, &fraudType, &status, &m.RequestID, &m.ReportID, &m.Reporter,
		&m.Amount, &m.Created, &m.Modified)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.FraudMarker{}, dict.ErrNotFound
	}
	m.FraudType, m.Status = dict.FraudType(fraudType), dict.MarkerStatus(status)
	m.Created, m.Modified = m.Created.UTC(), m.Modified.UTC()
	return m, err
}

func (m medTx) Marker(ctx context.Context, id string) (dict.FraudMarker, error) {
	if !isUUID(id) {
		return dict.FraudMarker{}, dict.ErrNotFound
	}
	return scanMarker(m.tx.QueryRow(ctx, `SELECT `+markerColumns+` FROM fraud_markers WHERE id = $1 FOR UPDATE`, id))
}

func (m medTx) MarkerByRequestID(ctx context.Context, participant, requestID string) (dict.FraudMarker, error) {
	if !isUUID(requestID) {
		return dict.FraudMarker{}, dict.ErrNotFound
	}
	return scanMarker(m.tx.QueryRow(ctx, `SELECT `+markerColumns+` FROM fraud_markers WHERE registrant = $1 AND request_id = $2
		FOR UPDATE`, participant, requestID))
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (m medTx) SaveMarker(ctx context.Context, f dict.FraudMarker) error {
	_, err := m.tx.Exec(ctx, `
		INSERT INTO fraud_markers (id, registrant, tax_id, key, fraud_type, status, request_id, report_id, reporter, amount_cents,
			created, modified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET status = $6, modified = $12`,
		f.ID, f.Registrant, f.TaxID, f.Key, f.FraudType, f.Status, nullIfEmpty(f.RequestID), nullIfEmpty(f.ReportID), f.Reporter,
		f.Amount, f.Created, f.Modified)
	return err
}

func (r *Repository) ListMarkers(ctx context.Context, q dict.MarkerQuery) ([]dict.FraudMarker, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+markerColumns+` FROM fraud_markers
		WHERE ($1 = '' OR tax_id = $1) AND ($2 = '' OR key = $2) ORDER BY modified, id`, q.TaxID, q.Key)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.FraudMarker, error) { return scanMarker(row) })
}

// --- refunds ---

const refundColumns = `id::text, transaction_id, reason, amount_cents, details, status, requesting, contested,
	COALESCE(infraction_report_id::text, ''), analysis_result, analysis_details, rejection_reason, refund_transaction_id,
	created, modified`

func scanRefund(row pgx.Row) (dict.Refund, error) {
	var f dict.Refund
	var reason, status, result, rejection string
	err := row.Scan(&f.ID, &f.TransactionID, &reason, &f.Amount, &f.Details, &status, &f.Requesting, &f.Contested,
		&f.InfractionReportID, &result, &f.AnalysisDetails, &rejection, &f.RefundTransactionID, &f.Created, &f.Modified)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.Refund{}, dict.ErrNotFound
	}
	f.Reason, f.Status, f.AnalysisResult = dict.RefundReason(reason), dict.RefundStatus(status), dict.RefundResult(result)
	f.RejectionReason = dict.RejectionReason(rejection)
	f.Created, f.Modified = f.Created.UTC(), f.Modified.UTC()
	return f, err
}

func collectRefunds(rows pgx.Rows, err error) ([]dict.Refund, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.Refund, error) { return scanRefund(row) })
}

func (m medTx) Refund(ctx context.Context, id string) (dict.Refund, error) {
	if !isUUID(id) {
		return dict.Refund{}, dict.ErrNotFound
	}
	return scanRefund(m.tx.QueryRow(ctx, `SELECT `+refundColumns+` FROM refunds WHERE id = $1 FOR UPDATE`, id))
}

func (m medTx) RefundsOf(ctx context.Context, transactionID string) ([]dict.Refund, error) {
	return collectRefunds(m.tx.Query(ctx, `SELECT `+refundColumns+` FROM refunds WHERE transaction_id = $1
		ORDER BY modified, id FOR UPDATE`, transactionID))
}

func (m medTx) SaveRefund(ctx context.Context, f dict.Refund) error {
	_, err := m.tx.Exec(ctx, `
		INSERT INTO refunds (id, transaction_id, reason, amount_cents, details, status, requesting, contested,
			infraction_report_id, analysis_result, analysis_details, rejection_reason, refund_transaction_id, created, modified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (id) DO UPDATE SET status = $6, analysis_result = $10, analysis_details = $11, rejection_reason = $12,
			refund_transaction_id = $13, modified = $15`,
		f.ID, f.TransactionID, f.Reason, f.Amount, f.Details, f.Status, f.Requesting, f.Contested, nullIfEmpty(f.InfractionReportID),
		f.AnalysisResult, f.AnalysisDetails, f.RejectionReason, f.RefundTransactionID, f.Created, f.Modified)
	return err
}

func (r *Repository) ListRefunds(ctx context.Context, participant string) ([]dict.Refund, error) {
	return collectRefunds(r.pool.Query(ctx, `SELECT `+refundColumns+` FROM refunds
		WHERE requesting = $1 OR contested = $1 ORDER BY modified, id`, participant))
}

// --- entries of a user ---

func (r *Repository) EntriesOf(ctx context.Context, taxID string) ([]dict.Entry, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+selectColumns+` FROM entries WHERE owner_tax_id = $1 ORDER BY key`, taxID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.Entry, error) { return scanEntry(row) })
}

// isUUID guards the uuid columns: a path id that is not a UUID is simply not found, instead of a cast error.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f', 'A' <= c && c <= 'F':
		default:
			return false
		}
	}
	return true
}
