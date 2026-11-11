package dict

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxKeyLength       = 77
	maxNameLength      = 150
	maxTradeNameLength = 100
)

// Expressões regulares copiadas da especificação do DICT (v2.13.0).
var (
	keyRegexps = map[KeyType]*regexp.Regexp{
		KeyCPF:   regexp.MustCompile(`^[0-9]{11}$`),
		KeyCNPJ:  regexp.MustCompile(`^[A-Z0-9]{12}[0-9]{2}$`),
		KeyPhone: regexp.MustCompile(`^\+[1-9]\d{1,14}$`),
		KeyEmail: regexp.MustCompile("^[a-z0-9.!#$'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$"),
		KeyEVP:   regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`),
	}
	// A ordem não importa: os formatos são disjuntos.
	keyTypes = []KeyType{KeyCPF, KeyCNPJ, KeyPhone, KeyEmail, KeyEVP}

	ispbRe          = regexp.MustCompile(`^[A-Z0-9]{8}$`)
	branchRe        = regexp.MustCompile(`^[0-9]{1,4}$`)
	accountNumberRe = regexp.MustCompile(`^[A-Z0-9]{1,20}$`)
	cpfRe           = keyRegexps[KeyCPF]
	cnpjRe          = keyRegexps[KeyCNPJ]
	naturalNameRe   = regexp.MustCompile(`^[A-Za-zÀ-ÖØ-öø-ÿ' -]+$`)
	legalNameRe     = regexp.MustCompile(`^[\x{20}-\x{7E}\x{A1}-\x{FF}]+$`)
	requestIDRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

// ValidISPB informa se s é um identificador SPB válido (8 caracteres A-Z/0-9).
func ValidISPB(s string) bool { return ispbRe.MatchString(s) }

// DetectKeyType descobre o tipo de uma chave a partir do seu formato.
func DetectKeyType(key string) (KeyType, bool) {
	if len(key) > maxKeyLength {
		return "", false
	}
	for _, kt := range keyTypes {
		if keyRegexps[kt].MatchString(key) {
			return kt, true
		}
	}
	return "", false
}

func validKeyType(kt KeyType) bool {
	_, ok := keyRegexps[kt]
	return ok
}

func validAccountType(t AccountType) bool {
	switch t {
	case AccountChecking, AccountTransact, AccountSalary, AccountSavings, AccountOther:
		return true
	}
	return false
}

func validateKey(property string, kt KeyType, key string, v *violations) {
	if len(key) > maxKeyLength {
		v.add(property, key, fmt.Sprintf("Value exceeds %d characters", maxKeyLength))
		return
	}
	if re := keyRegexps[kt]; !re.MatchString(key) {
		v.add(property, key, fmt.Sprintf("Value does not match regex '%s' (%s)", re, kt))
	}
}

func validateRegexp(property, value string, re *regexp.Regexp, v *violations) {
	if !re.MatchString(value) {
		v.add(property, value, fmt.Sprintf("Value does not match regex '%s'", re))
	}
}

func validateAccount(prefix string, a Account, v *violations) {
	validateRegexp(prefix+".participant", a.Participant, ispbRe, v)
	if a.Branch != "" {
		validateRegexp(prefix+".branch", a.Branch, branchRe, v)
	}
	validateRegexp(prefix+".accountNumber", a.AccountNumber, accountNumberRe, v)
	if !validAccountType(a.Type) {
		v.add(prefix+".accountType", string(a.Type), "Unknown account type")
	}
	if a.OpeningDate.IsZero() {
		v.add(prefix+".openingDate", "", "Value is required")
	}
}

func validateOwner(prefix string, o Owner, v *violations) {
	switch o.Type {
	case NaturalPerson:
		validateRegexp(prefix+".taxIdNumber", o.TaxID, cpfRe, v)
		validateName(prefix+".name", o.Name, maxNameLength, naturalNameRe, v)
		if o.TradeName != "" {
			v.add(prefix+".tradeName", o.TradeName, "Trade name is only allowed for LEGAL_PERSON")
		}
	case LegalPerson:
		validateRegexp(prefix+".taxIdNumber", o.TaxID, cnpjRe, v)
		validateName(prefix+".name", o.Name, maxNameLength, legalNameRe, v)
		if o.TradeName != "" {
			validateName(prefix+".tradeName", o.TradeName, maxTradeNameLength, legalNameRe, v)
			if strings.Trim(o.TradeName, "*") == "" {
				v.add(prefix+".tradeName", o.TradeName, "Value must not consist only of '*'")
			}
		}
	default:
		v.add(prefix+".type", string(o.Type), "Unknown person type")
	}
}

func validateName(property, name string, max int, re *regexp.Regexp, v *violations) {
	switch {
	case name == "":
		v.add(property, name, "Value is required")
	case utf8.RuneCountInString(name) > max:
		v.add(property, name, fmt.Sprintf("Value exceeds %d characters", max))
	default:
		validateRegexp(property, name, re, v)
	}
}

// validateKeyOwner garante que chaves CPF/CNPJ pertençam ao próprio titular.
func validateKeyOwner(kt KeyType, key string, o Owner, v *violations) {
	switch {
	case kt == KeyCPF && (o.Type != NaturalPerson || o.TaxID != key):
		v.add("entry.owner.taxIdNumber", o.TaxID, "CPF key must belong to a NATURAL_PERSON with the same tax id")
	case kt == KeyCNPJ && (o.Type != LegalPerson || o.TaxID != key):
		v.add("entry.owner.taxIdNumber", o.TaxID, "CNPJ key must belong to a LEGAL_PERSON with the same tax id")
	}
}

func allowed(r Reason, list ...Reason) bool {
	for _, x := range list {
		if r == x {
			return true
		}
	}
	return false
}

func createReasons() []Reason {
	return []Reason{ReasonUserRequested, ReasonReconciliation}
}

func updateReasons(kt KeyType) []Reason {
	if kt == KeyEVP {
		return []Reason{ReasonBranchTransfer, ReasonReconciliation, ReasonRFBValidation}
	}
	return []Reason{ReasonUserRequested, ReasonBranchTransfer, ReasonReconciliation, ReasonRFBValidation}
}

func deleteReasons() []Reason {
	return []Reason{ReasonUserRequested, ReasonAccountClosure, ReasonReconciliation, ReasonFraud, ReasonRFBValidation}
}

func validateReason(property string, r Reason, valid []Reason, v *violations) {
	if !allowed(r, valid...) {
		v.add(property, string(r), fmt.Sprintf("Invalid reason, valid values: %v", valid))
	}
}
