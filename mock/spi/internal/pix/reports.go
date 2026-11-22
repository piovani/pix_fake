package pix

import (
	"context"
	"errors"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/spi"
)

// Participants report the Pix transactions they settle in their own systems (for example a payment between
// two customers of the same institution) with a trck.002, so the funds can be traced in fraud cases. The SPI
// answers with a camt.025: one receipt per reported transaction, accepted (ACPT) or rejected (RJCT) with a reason.
//
//	FF08  the transaction settled more than 30 days ago, or the date is in the future
//	AM01  zero amount
//	AM18  no transactions, or more than 500
//	DT02  invalid message date
//	RC09  the payer's participant is not the sender, or is not registered
//	RC10  the receiver's participant is not registered
//	AG01  the participants are different (an "external transfer": it must go through the SPI)
//	ED05  any other problem with the content, or a transaction that was settled through the SPI

// reportLimit is how long after settling a transaction it can still be reported (Manual de Tempos do Pix).
const reportLimit = 30 * 24 * time.Hour

// maxReported is the most transactions a trck.002 carries.
const maxReported = 500

// ErrReportedNotFound is returned when no transaction was reported with the ID.
var ErrReportedNotFound = errors.New("reported transaction not found")

// ReportedTransaction is a transaction a participant settled in its own systems and reported to the SPI.
type ReportedTransaction struct {
	// ID identifies it: the RtrId when it is a return, otherwise the EndToEndId.
	ID         string
	EndToEndID string
	Scenario   string // BOK1 or BOK2
	Initiation string // MANU, DICT...

	AmountCents int64
	SettledAt   time.Time

	PayerISPB, PayerTaxID, PayerBranch, PayerAccount, PayerAccountType string
	PayeeISPB, PayeeTaxID, PayeeBranch, PayeeAccount, PayeeAccountType string
	PayeeKey                                                           string

	ReportedBy string // the participant that sent the report
	ReportedAt time.Time
}

// ReportStore keeps the reported transactions.
type ReportStore interface {
	// AddReported stores the transaction. When one with the same ID was already reported it stores
	// nothing and returns added == false.
	AddReported(ctx context.Context, t ReportedTransaction) (added bool, err error)
	// GetReported returns the transaction or ErrReportedNotFound.
	GetReported(ctx context.Context, id string) (ReportedTransaction, error)
}

// Storage is everything the Processor keeps.
type Storage interface {
	PaymentStore
	ReportStore
}

func (p *Processor) handleTrck002(ctx context.Context, sender string, m spi.Message) {
	msg, err := iso.ParseTrck002(m.Body)
	if err != nil {
		p.sendError(ctx, sender, m, "XML inválido", "Document", err.Error())
		return
	}
	if !msgIDPattern.MatchString(msg.MsgID()) {
		// A receipt must name the message it answers, so this one gets an admi.002 instead.
		p.sendError(ctx, sender, m, "Identificador de mensagem inválido", "GrpHdr/MsgId", "invalid MsgId")
		return
	}

	group := reportGroupCheck(msg, sender)
	var receipts []iso.Receipt
	for _, tx := range msg.Transactions() {
		if !ValidEndToEndID(tx.PmtID.EndToEnd) || (tx.PmtID.InstrID != "" && !ValidReturnID(tx.PmtID.InstrID)) {
			p.sendError(ctx, sender, m, "Identificador inválido", "Tx/PmtId", "malformed EndToEndId or InstrId")
			continue
		}
		rj := group
		if rj == nil {
			rj = p.storeReported(ctx, tx, sender)
		}
		if rj == nil {
			receipts = append(receipts, iso.NewReceipt(msg.MsgID(), tx.ID(), "", ""))
		} else {
			receipts = append(receipts, iso.NewReceipt(msg.MsgID(), tx.ID(), rj.code, rj.detail))
		}
	}
	if len(receipts) == 0 {
		return
	}
	body, err := iso.NewCamt025(SPIISPB, sender, newMsgID(), iso.FormatTime(p.now()), receipts).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "encode camt.025", "error", err)
		return
	}
	if _, err := p.svc.Deliver(ctx, sender, body); err != nil {
		p.log.ErrorContext(ctx, "deliver camt.025", "error", err)
	}
}

// reportGroupCheck validates what is common to the transactions of a trck.002.
func reportGroupCheck(m *iso.Trck002, sender string) *rejection {
	switch n := len(m.Transactions()); {
	case !validTime(m.CreatedAt()):
		return reject("DT02", "invalid CreDtTm")
	case n == 0 || n > maxReported:
		return reject("AM18", "a trck.002 carries between 1 and 500 transactions")
	case m.Status() != iso.StatusTracked:
		return reject("ED05", "the status of the transactions must be "+iso.StatusTracked)
	case m.AppHdr.Fr.ISPB() != sender:
		return reject("RC09", "AppHdr sender does not match the participant that sent the message")
	}
	return nil
}

// storeReported validates a reported transaction and stores it. A nil result means it was recorded (or had been before).
func (p *Processor) storeReported(ctx context.Context, tx iso.TrackedTx, sender string) *rejection {
	rep, rj := p.checkReported(ctx, tx, sender)
	if rj != nil {
		return rj
	}
	if _, err := p.reports.AddReported(ctx, rep); err != nil {
		p.log.ErrorContext(ctx, "store reported transaction", "id", rep.ID, "error", err)
		return reject("ED05", "could not record the transaction")
	}
	return nil
}

func (p *Processor) checkReported(ctx context.Context, tx iso.TrackedTx, sender string) (ReportedTransaction, *rejection) {
	var zero ReportedTransaction
	cents, ok := ParseAmount(tx.Amount.Value)
	switch {
	case tx.Amount.Ccy != "BRL" || !ok:
		return zero, reject("ED05", "invalid amount")
	case cents == 0:
		return zero, reject("AM01", "amount is zero")
	}

	settledAt, err := iso.ParseTime(tx.SettledAt())
	switch {
	case err != nil:
		return zero, reject("FF08", "invalid settlement date and time")
	case settledAt.After(p.now().Add(5 * time.Minute)):
		return zero, reject("FF08", "the settlement date is in the future")
	case p.now().Sub(settledAt) > reportLimit:
		return zero, reject("FF08", "the transaction settled more than 30 days ago")
	}

	if rj := checkReportedContent(tx); rj != nil {
		return zero, rj
	}

	payer, payee := tx.DebtorAgent.ISPB(), tx.CreditorAgent.ISPB()
	switch {
	case !ispbPattern.MatchString(payer) || !ispbPattern.MatchString(payee):
		return zero, reject("ED05", "invalid participant ISPB")
	case payer != sender:
		return zero, reject("RC09", "the payer's participant is not the sender")
	case !p.participants.Known(payer):
		return zero, reject("RC09", "payer participant is not registered")
	case !p.participants.Known(payee):
		return zero, reject("RC10", "receiver participant is not registered")
	case tx.PmtScnro.Prtry == "BOK1" && payer != payee:
		return zero, reject("ED05", "scenario BOK1 needs the same participant on both sides")
	case tx.PmtScnro.Prtry == "BOK2" && payer == payee:
		return zero, reject("ED05", "scenario BOK2 needs different participants")
	case tx.PmtScnro.Prtry == "BOK2":
		// Every participant of the mock settles for itself, so a payment between two of them is an
		// external transfer: it has to go through the SPI.
		return zero, reject("AG01", "payments between participants that do not settle together go through the SPI")
	}

	// A transaction settled through the SPI is not "outside" it.
	if _, err := p.payments.GetPayment(ctx, tx.ID()); err == nil {
		return zero, reject("ED05", "the transaction was settled through the SPI")
	}

	return ReportedTransaction{
		ID: tx.ID(), EndToEndID: tx.PmtID.EndToEnd, Scenario: tx.PmtScnro.Prtry, Initiation: tx.Initiation(),
		AmountCents: cents, SettledAt: settledAt,
		PayerISPB: payer, PayerTaxID: tx.Debtor.TaxID(), PayerBranch: tx.DebtorAcct.Branch(),
		PayerAccount: tx.DebtorAcct.Number(), PayerAccountType: tx.DebtorAcct.Type(),
		PayeeISPB: payee, PayeeTaxID: tx.Creditor.TaxID(), PayeeBranch: tx.CreditorAcct.Branch(),
		PayeeAccount: tx.CreditorAcct.Number(), PayeeAccountType: tx.CreditorAcct.Type(), PayeeKey: tx.CreditorAcct.Key(),
		ReportedBy: sender, ReportedAt: p.now(),
	}, nil
}

// checkReportedContent checks how it was initiated, the people and the accounts.
func checkReportedContent(tx iso.TrackedTx) *rejection {
	form := tx.Initiation()
	if !oneOf(form, "APDN", "APES", "AUTO", "DICT", "INIC", "MANU", "QRDN", "QRES") {
		return reject("ED05", "unknown initiation form")
	}
	if tx.PmtID.InstrID != "" && form != "MANU" {
		return reject("ED05", "a return is reported with initiation MANU")
	}
	hasKey := tx.CreditorAcct.Key() != ""
	if hasKey == oneOf(form, "MANU", "AUTO") {
		return reject("ED05", "Prxy must be absent for MANU and AUTO, and present for the other initiation forms")
	}
	if !oneOf(tx.PmtScnro.Prtry, "BOK1", "BOK2") {
		return reject("ED05", "unknown payment scenario")
	}
	if !validTaxID(tx.Debtor.TaxID()) || !validTaxID(tx.Creditor.TaxID()) {
		return reject("ED05", "CPF or CNPJ is invalid")
	}
	for _, a := range []struct {
		who  string
		acct iso.Account
	}{{"payer", tx.DebtorAcct}, {"receiver", tx.CreditorAcct}} {
		if n := a.acct.Number(); n == "" || len(n) > 20 {
			return reject("ED05", "invalid %s account number", a.who)
		}
		if b := a.acct.Branch(); b != "" && !branchPattern.MatchString(b) {
			return reject("ED05", "invalid %s branch", a.who)
		}
		if !oneOf(a.acct.Type(), "CACC", "SVGS", "TRAN", "OTHR") {
			return reject("ED05", "invalid %s account type", a.who)
		}
	}
	return nil
}
