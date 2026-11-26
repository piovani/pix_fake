package dict

import (
	"context"
	"time"
)

// AddTransaction tells the DICT of a transaction settled in the SPI, which the real DICT learns from the SPI itself.
// It is how tests and the control API make a transaction reportable. Adding it again replaces it.
func (s *Service) AddTransaction(ctx context.Context, t Transaction) error {
	var v violations
	validateRegexp("id", t.ID, refundTransactionRe, &v) // an EndToEndId
	validateRegexp("debtor.participant", t.DebtorParticipant, ispbRe, &v)
	validateRegexp("creditor.participant", t.CreditorParticipant, ispbRe, &v)
	validateRegexp("debtor.taxIdNumber", t.DebtorTaxID, taxIDRe, &v)
	validateRegexp("creditor.taxIdNumber", t.CreditorTaxID, taxIDRe, &v)
	if t.CreditorKey != "" {
		if _, ok := DetectKeyType(t.CreditorKey); !ok {
			v.add("creditor.key", t.CreditorKey, "Value is not a valid CPF, CNPJ, PHONE, EMAIL or EVP key")
		}
	}
	if t.Amount <= 0 {
		v.add("amount", FormatAmount(t.Amount), "Value must be greater than zero")
	}
	if t.SettlementTime.IsZero() {
		t.SettlementTime = s.stamp()
	}
	if len(v) > 0 {
		return invalid(KindBadRequest, "Transaction has invalid fields", v)
	}
	t.SettlementTime = t.SettlementTime.UTC().Truncate(time.Millisecond)
	return s.repo.AddTransaction(ctx, t)
}

// Transactions lists the transactions the DICT knows (control API).
func (s *Service) Transactions(ctx context.Context) ([]Transaction, error) {
	return s.repo.ListTransactions(ctx, TransactionQuery{})
}
