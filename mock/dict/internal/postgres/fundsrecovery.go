package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"pixfake/mock/dict/internal/dict"
)

// --- the transactions the tracking follows ---

func (m medTx) TransactionsFrom(ctx context.Context, participant, taxID string) ([]dict.Transaction, error) {
	rows, err := m.tx.Query(ctx, `SELECT `+transactionColumns+` FROM transactions
		WHERE debtor_participant = $1 AND debtor_tax_id = $2 ORDER BY settlement_time, id FOR UPDATE`, participant, taxID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.Transaction, error) { return scanTransaction(row) })
}

// --- funds recoveries ---

const recoveryColumns = `id::text, root_transaction_id, situation_type, report_details, contact_email, contact_phone, status,
	reporter, graph, created, modified`

func scanRecovery(row pgx.Row) (dict.FundsRecovery, error) {
	var (
		f                 dict.FundsRecovery
		situation, status string
		email, phone      string
		graph             []byte
	)
	err := row.Scan(&f.ID, &f.RootTransactionID, &situation, &f.ReportDetails, &email, &phone, &status, &f.Reporter, &graph,
		&f.Created, &f.Modified)
	if errors.Is(err, pgx.ErrNoRows) {
		return dict.FundsRecovery{}, dict.ErrNotFound
	}
	if err != nil {
		return dict.FundsRecovery{}, err
	}
	if err := json.Unmarshal(graph, &f.Graph); err != nil {
		return dict.FundsRecovery{}, err
	}
	f.Situation, f.Status = dict.SituationType(situation), dict.RecoveryStatus(status)
	f.Contact = &dict.Contact{Email: email, Phone: phone}
	f.Created, f.Modified, f.Graph.Created = f.Created.UTC(), f.Modified.UTC(), f.Graph.Created.UTC()
	for i := range f.Graph.Transactions {
		f.Graph.Transactions[i].SettlementTime = f.Graph.Transactions[i].SettlementTime.UTC()
	}
	return f, nil
}

func (m medTx) FundsRecovery(ctx context.Context, id string) (dict.FundsRecovery, error) {
	if !isUUID(id) {
		return dict.FundsRecovery{}, dict.ErrNotFound
	}
	return scanRecovery(m.tx.QueryRow(ctx, `SELECT `+recoveryColumns+` FROM funds_recoveries WHERE id = $1 FOR UPDATE`, id))
}

func (m medTx) FundsRecoveryOf(ctx context.Context, rootTransactionID string) (dict.FundsRecovery, error) {
	return scanRecovery(m.tx.QueryRow(ctx, `SELECT `+recoveryColumns+` FROM funds_recoveries WHERE root_transaction_id = $1
		FOR UPDATE`, rootTransactionID))
}

func (m medTx) SaveFundsRecovery(ctx context.Context, f dict.FundsRecovery) error {
	graph, err := json.Marshal(f.Graph)
	if err != nil {
		return err
	}
	var email, phone string
	if f.Contact != nil {
		email, phone = f.Contact.Email, f.Contact.Phone
	}
	_, err = m.tx.Exec(ctx, `
		INSERT INTO funds_recoveries (id, root_transaction_id, situation_type, report_details, contact_email, contact_phone,
			status, reporter, graph, created, modified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET situation_type = $3, report_details = $4, contact_email = $5, contact_phone = $6,
			status = $7, modified = $11`,
		f.ID, f.RootTransactionID, f.Situation, f.ReportDetails, email, phone, f.Status, f.Reporter, graph, f.Created, f.Modified)
	return err
}

func lockClause(lock bool) string {
	if lock {
		return ` FOR UPDATE`
	}
	return ``
}

func (m medTx) ReportsOfRecovery(ctx context.Context, recoveryID string, lock bool) ([]dict.InfractionReport, error) {
	if !isUUID(recoveryID) {
		return nil, nil
	}
	return collectReports(m.tx.Query(ctx, `SELECT `+reportColumns+` FROM infraction_reports WHERE funds_recovery_id = $1
		ORDER BY created, id`+lockClause(lock), recoveryID))
}

func (m medTx) RefundsOfRecovery(ctx context.Context, recoveryID string, lock bool) ([]dict.Refund, error) {
	if !isUUID(recoveryID) {
		return nil, nil
	}
	return collectRefunds(m.tx.Query(ctx, `SELECT `+refundColumns+` FROM refunds WHERE funds_recovery_id = $1
		ORDER BY created, id`+lockClause(lock), recoveryID))
}

// --- event notifications ---

func (m medTx) AddEvent(ctx context.Context, e dict.Event) error {
	_, err := m.tx.Exec(ctx, `INSERT INTO event_notifications (event, entity_id, participant, at) VALUES ($1, $2, $3, $4)`,
		e.Type, e.EntityID, e.Participant, e.Time)
	return err
}

func (r *Repository) ListEvents(ctx context.Context, participant string, afterID int64, since time.Time, limit int) ([]dict.Event, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, event, entity_id::text, participant, at FROM event_notifications
		WHERE participant = $1 AND id > $2 AND at >= $3 ORDER BY id LIMIT $4`, participant, afterID, since, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dict.Event, error) {
		var e dict.Event
		var event string
		err := row.Scan(&e.ID, &event, &e.EntityID, &e.Participant, &e.Time)
		e.Type, e.Time = dict.EventType(event), e.Time.UTC()
		return e, err
	})
}
