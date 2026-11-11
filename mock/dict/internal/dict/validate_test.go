package dict

import "testing"

func TestDetectKeyType(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want KeyType
		ok   bool
	}{
		{"cpf", "11122233300", KeyCPF, true},
		{"numeric cnpj", "12345678901234", KeyCNPJ, true},
		{"alphanumeric cnpj", "AB12CD34EF5601", KeyCNPJ, true},
		{"phone", "+5561988887777", KeyPhone, true},
		{"email", "pix@bcb.gov.br", KeyEmail, true},
		{"evp", "123e4567-e89b-12d3-a456-426655440000", KeyEVP, true},
		{"phone without +", "5561988887777", "", false},
		{"phone starting with 0", "+0561988887777", "", false},
		{"uppercase email", "Pix@bcb.gov.br", "", false},
		{"email with %", "pi%x@bcb.gov.br", "", false},
		{"email over 77 characters", repeat("a", 70) + "@bcb.gov.br", "", false},
		{"uppercase evp", "123E4567-E89B-12D3-A456-426655440000", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DetectKeyType(tt.key)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DetectKeyType(%q) = %q, %v; want %q, %v", tt.key, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestValidateOwner(t *testing.T) {
	tests := []struct {
		name  string
		owner Owner
		bad   bool
	}{
		{"natural person ok", Owner{Type: NaturalPerson, TaxID: "11122233300", Name: "João D'Ávila-Silva"}, false},
		{"natural person with trade name", Owner{Type: NaturalPerson, TaxID: "11122233300", Name: "João", TradeName: "x"}, true},
		{"natural person with invalid cpf", Owner{Type: NaturalPerson, TaxID: "123", Name: "João"}, true},
		{"natural person name with digit", Owner{Type: NaturalPerson, TaxID: "11122233300", Name: "João 2"}, true},
		{"legal person ok", Owner{Type: LegalPerson, TaxID: "11222333000150", Name: "Padaria Tres Irmaos Ltda", TradeName: "Padaria 3 Irmãos"}, false},
		{"legal person trade name only asterisks", Owner{Type: LegalPerson, TaxID: "11222333000150", Name: "Padaria", TradeName: "***"}, true},
		{"legal person empty name", Owner{Type: LegalPerson, TaxID: "11222333000150"}, true},
		{"unknown type", Owner{Type: "X", TaxID: "11122233300", Name: "João"}, true},
		{"name over 150 characters", Owner{Type: NaturalPerson, TaxID: "11122233300", Name: repeat("a", 151)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v violations
			validateOwner("owner", tt.owner, &v)
			if got := len(v) > 0; got != tt.bad {
				t.Fatalf("violations = %v, want invalid=%v", v, tt.bad)
			}
		})
	}
}

func TestNewEVP(t *testing.T) {
	for range 100 {
		if kt, ok := DetectKeyType(NewEVP()); !ok || kt != KeyEVP {
			t.Fatal("NewEVP generated an invalid EVP key")
		}
	}
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}
