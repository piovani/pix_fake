package pix

import (
	"context"
	"crypto/rand"
	"log/slog"
	"strings"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/spi"
)

// Processor handles the payment messages participants send to the SPI.
//
//	pacs.008 (payer's participant)  -> validate, store, forward to the receiver
//	pacs.004 (receiving participant of an earlier payment, returning it) -> same cycle, see returns.go
//	pacs.002 (receiver, ACSP/RJCT)  -> settle or reject
//	trck.002 (any participant, transactions it settled itself) -> record, answer with a camt.025 (see reports.go)
//
// When the receiver is simulated the mock answers for it, so the whole cycle runs inside one call.
// A payment that the receiver does not answer within Timeout is rejected by the SPI (see ExpirePending).
// A message that cannot be processed at all is answered with an admi.002.
type Processor struct {
	// Timeout is how long a payment may wait for the receiving participant. The Pix limit is 40 seconds.
	Timeout time.Duration

	svc          *spi.Service
	payments     PaymentStore
	reports      ReportStore
	participants Participants
	responder    Responder
	log          *slog.Logger
	now          func() time.Time
}

var _ spi.Handler = (*Processor)(nil)

// idempotencyWindow is how long a repeated order gets the outcome it already has (catalog: idempotence with a
// 24-hour window). After that it is validated like a new one, and its EndToEndId is by then too old to pass.
const idempotencyWindow = 24 * time.Hour

func (p *Processor) insideIdempotencyWindow(pay Payment) bool {
	return p.now().Sub(pay.CreatedAt) <= idempotencyWindow
}

// DefaultTimeout is the time limit of a payment on the primary channel (Manual de Tempos do Pix).
const DefaultTimeout = 40 * time.Second

// NewProcessor creates the processor. store keeps payments and reports; responder decides for the simulated participants.
func NewProcessor(svc *spi.Service, store Storage, participants Participants, responder Responder, log *slog.Logger) *Processor {
	return &Processor{
		Timeout: DefaultTimeout,
		svc:     svc, payments: store, reports: store, participants: participants, responder: responder, log: log, now: time.Now,
	}
}

// SetClock replaces the clock, for tests.
func (p *Processor) SetClock(now func() time.Time) { p.now = now }

func (p *Processor) Handle(ctx context.Context, sender string, m spi.Message) {
	hdr, err := iso.Header(m.Body)
	if err != nil {
		p.sendError(ctx, sender, m, "XML inválido", "AppHdr", err.Error())
		return
	}
	switch hdr.MsgDefIdr {
	case iso.Pacs008ID:
		p.handlePacs008(ctx, sender, m)
	case iso.Pacs004ID:
		p.handlePacs004(ctx, sender, m)
	case iso.Trck002ID:
		p.handleTrck002(ctx, sender, m)
	case iso.Pacs002ID:
		p.handlePacs002(ctx, sender, m)
	default:
		p.sendError(ctx, sender, m, "Mensagem não suportada", "AppHdr/MsgDefIdr", hdr.MsgDefIdr+" is not supported by the mock")
	}
}

func (p *Processor) handlePacs008(ctx context.Context, sender string, m spi.Message) {
	msg, err := iso.ParsePacs008(m.Body)
	if err != nil {
		p.sendError(ctx, sender, m, "XML inválido", "Document", err.Error())
		return
	}
	group := groupCheck(msg, sender)
	for _, tx := range msg.Transactions() {
		e2e := tx.PmtID.EndToEnd
		if !ValidEndToEndID(e2e) {
			// A pacs.002 needs a valid identifier, so the answer is an admi.002.
			p.sendError(ctx, sender, m, "Identificador fim a fim inválido", "CdtTrfTxInf/PmtId/EndToEndId", "malformed EndToEndId")
			continue
		}

		// The SPI is idempotent by EndToEndId for 24 hours: a repeated order gets the outcome that already exists.
		if existing, err := p.payments.GetPayment(ctx, e2e); err == nil && p.insideIdempotencyWindow(existing) {
			p.resendOutcome(ctx, existing)
			continue
		}

		rj := group
		if rj == nil {
			rj = p.checkTransaction(tx, sender)
		}
		if rj != nil {
			p.sendRejection(ctx, sender, e2e, e2e, rj.code, rj.detail)
			continue
		}

		pay := p.newPayment(tx)
		existing, created, err := p.payments.CreatePayment(ctx, pay)
		if err != nil {
			p.log.Error("store payment", "end_to_end_id", e2e, "error", err)
			continue
		}
		if !created {
			p.resendOutcome(ctx, existing)
			continue
		}
		body, err := iso.NewPacs008(SPIISPB, pay.PayeeISPB, newMsgID(), iso.FormatTime(p.now()), "HIGH", "PAGPRI", tx).Marshal()
		if err != nil {
			p.log.Error("encode pacs.008", "end_to_end_id", e2e, "error", err)
			continue
		}
		p.forward(ctx, pay, body)
	}
}

func (p *Processor) newPayment(tx iso.CreditTransfer) Payment {
	cents, _ := ParseAmount(tx.Amount.Value)
	acceptedAt, _ := iso.ParseTime(tx.AcceptedAt)
	pay := Payment{
		ID:               tx.PmtID.EndToEnd,
		Kind:             KindPayment,
		EndToEndID:       tx.PmtID.EndToEnd,
		Status:           StatusPending,
		AmountCents:      cents,
		Purpose:          tx.Purpose.Cd,
		Initiation:       tx.Mandate.Tp.LclInstrm.Prtry,
		Priority:         "PAGPRI",
		PayerISPB:        tx.DebtorAgent.ISPB(),
		PayerName:        tx.Debtor.Name,
		PayerTaxID:       tx.Debtor.ID.TaxID(),
		PayerBranch:      tx.DebtorAcct.Branch(),
		PayerAccount:     tx.DebtorAcct.Number(),
		PayerAccountType: tx.DebtorAcct.Type(),
		PayeeISPB:        tx.CreditorAgent.ISPB(),
		PayeeTaxID:       tx.Creditor.ID.TaxID(),
		PayeeBranch:      tx.CreditorAcct.Branch(),
		PayeeAccount:     tx.CreditorAcct.Number(),
		PayeeAccountType: tx.CreditorAcct.Type(),
		PayeeKey:         tx.CreditorAcct.Key(),
		AcceptedAt:       acceptedAt,
		CreatedAt:        p.now(),
	}
	if tx.RmtInf != nil {
		pay.Info = tx.RmtInf.Ustrd
	}
	return pay
}

// forward delivers body (the order as the receiving participant gets it) to the receiving participant,
// or lets the mock answer when that participant is simulated.
func (p *Processor) forward(ctx context.Context, pay Payment, body []byte) {
	if p.participants.Simulated(pay.PayeeISPB) {
		if d := p.decide(ctx, pay); !d.NoAnswer {
			p.finalize(ctx, pay.ID, d)
		}
		return
	}
	if _, err := p.svc.Deliver(ctx, pay.PayeeISPB, body); err != nil {
		p.log.Error("deliver order", "id", pay.ID, "error", err)
	}
}

// decide is how a simulated receiving participant answers.
func (p *Processor) decide(ctx context.Context, pay Payment) Decision {
	if pay.Kind == KindReturn {
		return p.decideReturn(ctx, pay)
	}
	return p.responder.Decide(ctx, pay)
}

func (p *Processor) handlePacs002(ctx context.Context, sender string, m spi.Message) {
	msg, err := iso.ParsePacs002(m.Body)
	if err != nil {
		p.sendError(ctx, sender, m, "XML inválido", "Document", err.Error())
		return
	}
	for _, st := range msg.Statuses() {
		id := st.OrgnlInstrID // the EndToEndId of a payment, the RtrId of a return
		pay, err := p.payments.GetPayment(ctx, id)
		if err != nil {
			p.log.Warn("ignore status: unknown payment", "sender", sender, "id", id)
			continue
		}
		if pay.PayeeISPB != sender {
			p.log.Warn("ignore status: not from the receiver", "sender", sender, "id", id)
			continue
		}
		switch st.Status {
		case iso.StatusAcceptedByReceiver:
			p.finalize(ctx, id, Decision{Accept: true})
		case iso.StatusRejected:
			code := st.ReasonCode()
			if code == "" {
				code = "DS04"
			}
			p.finalize(ctx, id, Decision{ReasonCode: code})
		default:
			p.log.Warn("ignore status: unsupported", "sender", sender, "id", id, "status", st.Status)
		}
	}
}

// finalize applies the receiver's answer: settle and confirm to both sides, or reject.
func (p *Processor) finalize(ctx context.Context, id string, d Decision) {
	status := StatusRejected
	if d.Accept {
		status = StatusSettled
	}
	pay, changed, err := p.payments.FinalizePayment(ctx, id, status, d.ReasonCode, d.Detail, p.now())
	if err != nil {
		p.log.Error("finalize payment", "id", id, "status", status, "error", err)
		return
	}
	if !changed {
		return // already settled or rejected
	}
	if d.Accept {
		p.sendSettled(ctx, pay)
	} else {
		p.sendRejection(ctx, pay.PayerISPB, pay.ID, pay.EndToEndID, d.ReasonCode, d.Detail)
	}
}

// ExpirePending rejects, with AB03, the payments the receiving participant did not answer within
// Timeout, and tells the payer's participant. It returns how many it rejected.
func (p *Processor) ExpirePending(ctx context.Context) int {
	ids, err := p.payments.PendingBefore(ctx, p.now().Add(-p.Timeout))
	if err != nil {
		p.log.Error("expire pending", "error", err)
		return 0
	}
	for _, id := range ids {
		p.finalize(ctx, id, Decision{ReasonCode: "AB03", Detail: "no answer from the receiving participant within " + p.Timeout.String()})
	}
	return len(ids)
}

// RunExpiry calls ExpirePending every interval until ctx is done.
func (p *Processor) RunExpiry(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.ExpirePending(ctx)
		}
	}
}

// sendError answers a message that cannot be processed with an admi.002 to its sender. The
// message is referenced by the PI-ResourceId it was given when stored.
func (p *Processor) sendError(ctx context.Context, to string, m spi.Message, reason, location, description string) {
	p.log.Warn("message rejected", "sender", to, "resource_id", m.ResourceID, "reason", reason, "description", description)
	now := iso.FormatTime(p.now())
	body, err := iso.NewAdmi002(SPIISPB, to, newMsgID(), now, m.ResourceID, reason, location, description).Marshal()
	if err != nil {
		p.log.Error("encode admi.002", "error", err)
		return
	}
	if _, err := p.svc.Deliver(ctx, to, body); err != nil {
		p.log.Error("deliver admi.002", "error", err)
	}
}

// statusOf builds the status of a payment or return: the instruction is its ID, the EndToEndId that of the payment.
func statusOf(instrID, e2e, status string) iso.TxStatus {
	st := iso.NewTxStatus(e2e, status)
	st.OrgnlInstrID = instrID
	return st
}

// sendSettled tells the payer's participant (ACSC) and the receiver's (ACCC) that the transfer settled.
func (p *Processor) sendSettled(ctx context.Context, pay Payment) {
	at := pay.SettledAt
	for _, to := range []struct{ ispb, status string }{
		{pay.PayerISPB, iso.StatusSettledPayer},
		{pay.PayeeISPB, iso.StatusSettledReceiver},
	} {
		st := statusOf(pay.ID, pay.EndToEndID, to.status).WithSettlement(iso.FormatTime(at), at.UTC().Format(iso.DateLayout))
		p.sendStatus(ctx, to.ispb, st)
	}
}

func (p *Processor) sendRejection(ctx context.Context, to, instrID, e2e, code, detail string) {
	p.sendStatus(ctx, to, statusOf(instrID, e2e, iso.StatusRejected).WithReason(code, detail))
}

// resendOutcome answers a repeated order with the outcome it already has.
func (p *Processor) resendOutcome(ctx context.Context, pay Payment) {
	switch pay.Status {
	case StatusSettled:
		st := statusOf(pay.ID, pay.EndToEndID, iso.StatusSettledPayer).
			WithSettlement(iso.FormatTime(pay.SettledAt), pay.SettledAt.UTC().Format(iso.DateLayout))
		p.sendStatus(ctx, pay.PayerISPB, st)
	case StatusRejected:
		p.sendRejection(ctx, pay.PayerISPB, pay.ID, pay.EndToEndID, pay.ReasonCode, pay.ReasonDetail)
	}
}

func (p *Processor) sendStatus(ctx context.Context, to string, st iso.TxStatus) {
	now := iso.FormatTime(p.now())
	body, err := iso.NewPacs002(SPIISPB, to, newMsgID(), now, st).Marshal()
	if err != nil {
		p.log.Error("encode pacs.002", "id", st.OrgnlInstrID, "error", err)
		return
	}
	if _, err := p.svc.Deliver(ctx, to, body); err != nil {
		p.log.Error("deliver pacs.002", "id", st.OrgnlInstrID, "error", err)
	}
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// newMsgID returns a message identification from the SPI: M + SPI ISPB + 23 alphanumeric characters.
func newMsgID() string {
	var b [23]byte
	_, _ = rand.Read(b[:])
	var sb strings.Builder
	sb.WriteString("M" + SPIISPB)
	for _, v := range b {
		sb.WriteByte(idAlphabet[int(v)%len(idAlphabet)])
	}
	return sb.String()
}
