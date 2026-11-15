package pix

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pixfake/mock/spi/internal/iso"
)

// Rejection reason codes the SPI generates for a pacs.008 (see docs/catalog/data-contract.md).
const (
	reasonInvalidContent  = "CH16"
	reasonBadEndToEnd     = "FF08"
	reasonZeroAmount      = "AM01"
	reasonBadCount        = "AM18"
	reasonBookTransfer    = "AG12"
	reasonUnknownPayer    = "RC09"
	reasonUnknownReceiver = "RC10"
	reasonWrongAgent      = "AGNT"
	reasonNotSupported    = "AG03"
	reasonBadDate         = "DT02"
)

// endToEndTolerance is how far the date inside the EndToEndId may be from the processing time.
const endToEndTolerance = 12 * time.Hour

var (
	endToEndPattern = regexp.MustCompile(`^E[0-9A-Z]{8}[0-9]{4}[0-1][0-9][0-3][0-9][0-2][0-9][0-5][0-9][a-zA-Z0-9]{11}$`)
	ispbPattern     = regexp.MustCompile(`^[0-9A-Z]{8}$`)
	cpfPattern      = regexp.MustCompile(`^[0-9]{11}$`)
	cnpjPattern     = regexp.MustCompile(`^[0-9A-Z]{14}$`)
	branchPattern   = regexp.MustCompile(`^[0-9]{1,4}$`)
	amountPattern   = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)
	msgIDPattern    = regexp.MustCompile(`^M[0-9A-Z]{8}[a-zA-Z0-9]{23}$`)
)

// ValidEndToEndID reports whether s has the EndToEndId shape: E + ISPB (8) + yyyyMMddHHmm (12) + 11 alphanumeric.
func ValidEndToEndID(s string) bool { return endToEndPattern.MatchString(s) }

// rejection is a reason the SPI refuses an order for.
type rejection struct {
	code   string
	detail string
}

func reject(code, format string, args ...any) *rejection {
	return &rejection{code: code, detail: fmt.Sprintf(format, args...)}
}

// groupCheck validates what is common to all the transactions of a pacs.008. A non-nil
// result rejects every transaction in the message.
func groupCheck(m *iso.Pacs008, sender string) *rejection {
	g := m.Group()
	if !msgIDPattern.MatchString(g.MsgID) {
		return reject(reasonInvalidContent, "invalid MsgId")
	}
	if _, err := iso.ParseTime(g.CreDtTm); err != nil {
		return reject(reasonBadDate, "invalid CreDtTm")
	}
	n, err := strconv.Atoi(strings.TrimSpace(g.NbOfTxs))
	if err != nil || n != len(m.Transactions()) {
		return reject(reasonBadCount, "NbOfTxs does not match the number of transactions")
	}
	if g.SttlmInf.Method != "CLRG" {
		return reject(reasonInvalidContent, "SttlmMtd must be CLRG")
	}
	if g.PmtTpInf.Priority != "HIGH" || g.PmtTpInf.SvcLvl.Proprietary != "PAGPRI" {
		return reject(reasonInvalidContent, "only HIGH priority and PAGPRI are supported")
	}
	if m.AppHdr.Fr.ISPB() != sender {
		return reject(reasonWrongAgent, "AppHdr sender does not match the participant that sent the message")
	}
	return nil
}

// checkTransaction validates one transaction. A nil result means it can be processed. The order of
// the checks decides which reason wins when an order has several problems.
func (p *Processor) checkTransaction(tx iso.CreditTransfer, sender string) *rejection {
	if rj := checkEndToEnd(tx.PmtID.EndToEnd, p.now()); rj != nil {
		return rj
	}
	if rj := checkPayment(tx); rj != nil {
		return rj
	}
	if rj := checkParties(tx); rj != nil {
		return rj
	}
	return p.checkParticipants(tx, sender)
}

// checkEndToEnd checks the identifier and that its date is close to now.
func checkEndToEnd(e2e string, now time.Time) *rejection {
	if !ValidEndToEndID(e2e) {
		return reject(reasonBadEndToEnd, "malformed EndToEndId")
	}
	date, err := time.Parse("200601021504", e2e[9:21])
	if err != nil || date.Before(now.Add(-endToEndTolerance)) || date.After(now.Add(endToEndTolerance)) {
		return reject(reasonBadEndToEnd, "date of the EndToEndId is out of the accepted range")
	}
	return nil
}

// checkPayment checks the amount, how the payment was initiated and what the mock supports.
func checkPayment(tx iso.CreditTransfer) *rejection {
	if tx.Amount.Ccy != "BRL" || !amountPattern.MatchString(strings.TrimSpace(tx.Amount.Value)) {
		return reject(reasonInvalidContent, "invalid amount")
	}
	if cents, _ := ParseAmount(tx.Amount.Value); cents == 0 {
		return reject(reasonZeroAmount, "amount is zero")
	}
	if _, err := iso.ParseTime(tx.AcceptedAt); err != nil {
		return reject(reasonInvalidContent, "invalid AccptncDtTm")
	}
	if tx.ChrgBr != "SLEV" {
		return reject(reasonInvalidContent, "ChrgBr must be SLEV")
	}

	switch tx.Mandate.Tp.LclInstrm.Prtry {
	case "MANU":
		if tx.CreditorAcct.Key() != "" {
			return reject(reasonInvalidContent, "Prxy must be absent when the initiation is MANU")
		}
	case "DICT":
		if tx.CreditorAcct.Key() == "" {
			return reject(reasonInvalidContent, "Prxy is required when the initiation is DICT")
		}
	default:
		return reject(reasonInvalidContent, "only MANU and DICT initiation are supported")
	}
	if tx.Purpose.Cd != "IPAY" {
		return reject(reasonInvalidContent, "only purpose IPAY is supported")
	}
	if tx.InitgPty != nil || tx.Tax != nil || (tx.RmtInf != nil && tx.RmtInf.Strd != nil) {
		return reject(reasonInvalidContent, "InitgPty, Tax and structured remittance are not supported")
	}
	if tx.RmtInf != nil && len(tx.RmtInf.Ustrd) > 140 {
		return reject(reasonInvalidContent, "remittance information is longer than 140 characters")
	}
	return nil
}

// checkParties checks the payer, the receiver and their accounts.
func checkParties(tx iso.CreditTransfer) *rejection {
	if name := tx.Debtor.Name; name == "" || len(name) > 140 {
		return reject(reasonInvalidContent, "invalid payer name")
	}
	if !validTaxID(tx.Debtor.ID.TaxID()) || !validTaxID(tx.Creditor.ID.TaxID()) {
		return reject(reasonInvalidContent, "CPF or CNPJ is invalid")
	}
	for _, a := range []struct {
		who  string
		acct iso.Account
	}{{"payer", tx.DebtorAcct}, {"receiver", tx.CreditorAcct}} {
		if n := a.acct.Number(); n == "" || len(n) > 20 {
			return reject(reasonInvalidContent, "invalid %s account number", a.who)
		}
		if b := a.acct.Branch(); b != "" && !branchPattern.MatchString(b) {
			return reject(reasonInvalidContent, "invalid %s branch", a.who)
		}
	}
	if !oneOf(tx.DebtorAcct.Type(), "CACC", "SVGS", "TRAN") {
		return reject(reasonInvalidContent, "invalid payer account type")
	}
	switch t := tx.CreditorAcct.Type(); {
	case t == "SLRY":
		return reject(reasonNotSupported, "salary accounts do not receive payments")
	case !oneOf(t, "CACC", "SVGS", "TRAN"):
		return reject(reasonInvalidContent, "invalid receiver account type")
	}
	return nil
}

// checkParticipants checks the two participants against the sender and the registered ones.
func (p *Processor) checkParticipants(tx iso.CreditTransfer, sender string) *rejection {
	payer, receiver := tx.DebtorAgent.ISPB(), tx.CreditorAgent.ISPB()
	switch {
	case !ispbPattern.MatchString(payer) || !ispbPattern.MatchString(receiver):
		return reject(reasonInvalidContent, "invalid participant ISPB")
	case payer != sender:
		return reject(reasonWrongAgent, "the payer's participant is not the sender")
	case !p.participants.Known(payer):
		return reject(reasonUnknownPayer, "payer participant is not registered")
	case !p.participants.Known(receiver):
		return reject(reasonUnknownReceiver, "receiver participant is not registered")
	case payer == receiver:
		return reject(reasonBookTransfer, "payer and receiver are the same participant; a book transfer does not go through the SPI")
	}
	return nil
}

func validTaxID(s string) bool {
	return cpfPattern.MatchString(s) || (len(s) == 14 && cnpjPattern.MatchString(s))
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}

// ParseAmount converts "10.50" into cents. It accepts up to two decimals and 18 digits in total.
func ParseAmount(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if !amountPattern.MatchString(s) {
		return 0, false
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(whole)+2 > 18 {
		return 0, false
	}
	for len(frac) < 2 {
		frac += "0"
	}
	v, err := strconv.ParseInt(whole+frac, 10, 64)
	return v, err == nil
}
