package pix

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/spi"
)

// A return (pacs.004) sends money back to the payer of a settled payment. It is sent by the participant
// that received the payment and goes through the same cycle as a payment: the SPI forwards it to the
// participant of the original payer, which accepts or rejects it, and then settles.
//
//	SPI rejects:                    AG12 same participant, AG13 return of a return, DT05 past 90 days, and
//	                                the content rules shared with payments (AM01, FF08, AGNT, RC09, RC10, CH16)
//	original payer's participant:   AM09 amount above the original payment, plus the account rules of payments

// returnLimit is how long after settling a payment it can still be returned (regulated by the Pix scheme).
const returnLimit = 90 * 24 * time.Hour

// returnReason is the only return reason the mock supports: the receiver asked for the money back.
// The others (BE08, FR01, SL02) belong to the MED and to Pix Saque/Troco, which are out of scope.
const returnReason = "MD06"

var returnIDPattern = regexp.MustCompile(`^D[0-9A-Z]{8}[0-9]{4}[0-1][0-9][0-3][0-9][0-2][0-9][0-5][0-9][a-zA-Z0-9]{11}$`)

// ValidReturnID reports whether s has the RtrId shape: D + ISPB (8) + yyyyMMddHHmm (12) + 11 alphanumeric.
func ValidReturnID(s string) bool { return returnIDPattern.MatchString(s) }

func (p *Processor) handlePacs004(ctx context.Context, sender string, m spi.Message) {
	msg, err := iso.ParsePacs004(m.Body)
	if err != nil {
		p.sendError(ctx, sender, m, "XML inválido", "Document", err.Error())
		return
	}
	group := returnGroupCheck(msg, sender)
	for _, r := range msg.Returns() {
		tag(ctx, r.OrgnlEndToEnd, r.RtrID)
		if !ValidReturnID(r.RtrID) || !ValidEndToEndID(r.OrgnlEndToEnd) {
			// A pacs.002 needs both identifiers, so the answer is an admi.002.
			p.sendError(ctx, sender, m, "Identificador inválido", "TxInf/RtrId", "malformed RtrId or EndToEndId")
			continue
		}

		// The SPI is idempotent by RtrId: a repeated return gets the outcome that already exists.
		if existing, err := p.payments.GetPayment(ctx, r.RtrID); err == nil && p.insideIdempotencyWindow(existing) {
			p.resendOutcome(ctx, existing)
			continue
		}

		rj := group
		var pay Payment
		if rj == nil {
			pay, rj = p.buildReturn(ctx, r, sender)
		}
		if rj != nil {
			countRefused(ctx, KindReturn, rj.code)
			p.sendRejection(ctx, sender, r.RtrID, r.OrgnlEndToEnd, rj.code, rj.detail)
			continue
		}

		existing, created, err := p.payments.CreatePayment(ctx, pay)
		if err != nil {
			p.log.ErrorContext(ctx, "store return", "id", r.RtrID, "error", err)
			continue
		}
		if !created {
			p.resendOutcome(ctx, existing)
			continue
		}
		body, err := iso.NewPacs004(SPIISPB, pay.PayeeISPB, newMsgID(), iso.FormatTime(p.now()), r).Marshal()
		if err != nil {
			p.log.ErrorContext(ctx, "encode pacs.004", "id", r.RtrID, "error", err)
			continue
		}
		p.forward(ctx, pay, body)
	}
}

// returnGroupCheck validates what is common to all the returns of a pacs.004.
func returnGroupCheck(m *iso.Pacs004, sender string) *rejection {
	g := m.Group()
	switch {
	case !msgIDPattern.MatchString(g.MsgID):
		return reject(reasonInvalidContent, "invalid MsgId")
	case !validTime(g.CreDtTm):
		return reject(reasonBadDate, "invalid CreDtTm")
	case g.SttlmInf.Method != "CLRG":
		return reject(reasonInvalidContent, "SttlmMtd must be CLRG")
	case m.AppHdr.Fr.ISPB() != sender:
		return reject(reasonWrongAgent, "AppHdr sender does not match the participant that sent the message")
	}
	if n, err := strconv.Atoi(strings.TrimSpace(g.NbOfTxs)); err != nil || n != len(m.Returns()) {
		return reject(reasonBadCount, "NbOfTxs does not match the number of returns")
	}
	return nil
}

func validTime(s string) bool {
	_, err := iso.ParseTime(s)
	return err == nil
}

// buildReturn validates one return against the message and the original payment, and builds the transfer
// that goes back to the payer. A non-nil rejection is the reason the SPI refuses it.
func (p *Processor) buildReturn(ctx context.Context, r iso.Return, sender string) (Payment, *rejection) {
	if rj := checkReturnContent(r, p.now()); rj != nil {
		return Payment{}, rj
	}
	remetente, destinatario := r.OrgnlTxRef.DbtrAgt.ISPB(), r.OrgnlTxRef.CdtrAgt.ISPB()
	switch {
	case !ispbPattern.MatchString(remetente) || !ispbPattern.MatchString(destinatario):
		return Payment{}, reject(reasonInvalidContent, "invalid participant ISPB")
	case remetente != sender:
		return Payment{}, reject(reasonWrongAgent, "the participant that is debited is not the sender")
	case !p.participants.Known(remetente):
		return Payment{}, reject(reasonUnknownPayer, "sending participant is not registered")
	case !p.participants.Known(destinatario):
		return Payment{}, reject(reasonUnknownReceiver, "receiving participant is not registered")
	case remetente == destinatario:
		return Payment{}, reject(reasonBookTransfer, "both participants are the same; a book transfer does not go through the SPI")
	}

	orig, err := p.payments.GetPayment(ctx, r.OrgnlEndToEnd)
	if err != nil || orig.Kind != KindPayment || orig.Status != StatusSettled {
		return Payment{}, reject(reasonInvalidContent, "the original payment does not exist or did not settle")
	}
	if rj := p.checkDirection(ctx, orig, remetente, destinatario); rj != nil {
		return Payment{}, rj
	}
	if p.now().Sub(orig.SettledAt) > returnLimit {
		return Payment{}, reject(reasonPastDeadline, "the payment settled more than 90 days ago")
	}

	cents, _ := ParseAmount(r.Amount.Value)
	return Payment{
		ID:           r.RtrID,
		Kind:         KindReturn,
		EndToEndID:   orig.EndToEndID,
		ReturnReason: r.Reason(),
		Status:       StatusPending,
		AmountCents:  cents,
		Info:         r.Info(),
		Priority:     r.Priority,
		// The money goes from whoever received the payment back to whoever paid it.
		PayerISPB: orig.PayeeISPB, PayerTaxID: orig.PayeeTaxID, PayerBranch: orig.PayeeBranch,
		PayerAccount: orig.PayeeAccount, PayerAccountType: orig.PayeeAccountType,
		PayeeISPB: orig.PayerISPB, PayeeTaxID: orig.PayerTaxID, PayeeBranch: orig.PayerBranch,
		PayeeAccount: orig.PayerAccount, PayeeAccountType: orig.PayerAccountType,
		AcceptedAt: p.now(),
		CreatedAt:  p.now(),
	}, nil
}

// checkReturnContent checks the fields of the return itself.
func checkReturnContent(r iso.Return, now time.Time) *rejection {
	if !ValidReturnID(r.RtrID) {
		return reject(reasonBadEndToEnd, "malformed RtrId")
	}
	date, err := time.Parse("200601021504", r.RtrID[9:21])
	if err != nil || date.Before(now.Add(-endToEndTolerance)) || date.After(now.Add(endToEndTolerance)) {
		return reject(reasonBadEndToEnd, "date of the RtrId is out of the accepted range")
	}
	if r.Amount.Ccy != "BRL" || !amountPattern.MatchString(strings.TrimSpace(r.Amount.Value)) {
		return reject(reasonInvalidContent, "invalid amount")
	}
	if cents, _ := ParseAmount(r.Amount.Value); cents == 0 {
		return reject(reasonZeroAmount, "amount is zero")
	}
	switch {
	case r.Priority != "HIGH":
		return reject(reasonInvalidContent, "only HIGH priority is supported")
	case r.ChrgBr != "SLEV":
		return reject(reasonInvalidContent, "ChrgBr must be SLEV")
	case r.Reason() != returnReason:
		return reject(reasonInvalidContent, "only return reason "+returnReason+" is supported")
	case len(r.RtrRsnInf.AddtlInf) > 105:
		return reject(reasonInvalidContent, "return reason text is longer than 105 characters")
	case len(r.Info()) > 140:
		return reject(reasonInvalidContent, "remittance information is longer than 140 characters")
	}
	return nil
}

// checkDirection makes sure the return goes from the receiver of the original payment to its payer.
// The reverse direction is a return of a return when something was already returned (AG13).
func (p *Processor) checkDirection(ctx context.Context, orig Payment, remetente, destinatario string) *rejection {
	switch {
	case remetente == orig.PayeeISPB && destinatario == orig.PayerISPB:
		return nil
	case remetente == orig.PayerISPB && destinatario == orig.PayeeISPB:
		if returns, err := p.payments.ReturnsOf(ctx, orig.EndToEndID); err == nil {
			for _, ret := range returns {
				if ret.Status == StatusSettled {
					return reject(reasonReturnOfReturn, "a returned payment cannot be returned again")
				}
			}
		}
	}
	return reject(reasonInvalidContent, "the sender did not receive the original payment")
}

// decideReturn is how a simulated participant answers a return: it rejects one that makes the sum of the
// returns exceed the original payment (AM09), and otherwise applies the account rules of payments.
func (p *Processor) decideReturn(ctx context.Context, pay Payment) Decision {
	orig, err := p.payments.GetPayment(ctx, pay.EndToEndID)
	if err != nil {
		return decline("ED05", "could not read the original payment")
	}
	returns, err := p.payments.ReturnsOf(ctx, pay.EndToEndID)
	if err != nil {
		return decline("ED05", "could not read the returns of the payment")
	}
	var total int64
	for _, ret := range returns { // includes this return, which is pending
		if ret.Status != StatusRejected {
			total += ret.AmountCents
		}
	}
	if total > orig.AmountCents {
		return decline("AM09", "the returns add up to more than the original payment")
	}
	return p.responder.Decide(ctx, pay)
}
