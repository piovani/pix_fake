// Package pix is the payment logic of the SPI mock: it validates the pacs.008 orders,
// forwards them to the receiving participant and settles them.
package pix

// SPIISPB is the ISPB the SPI uses as sender of its messages (that of the Banco Central do Brasil).
const SPIISPB = "00038166"

// Participant is an institution that sends and receives Pix payments.
type Participant struct {
	ISPB string
	Name string
	// Simulated participants do not exist as clients: the mock answers for them.
	// Real ones (the wallet) read their own output stream and answer through the input.
	Simulated bool
}

// Participants is the set of institutions known to the SPI.
type Participants map[string]Participant

// DefaultParticipants are the fictional institutions of the shared seed (same ISPBs as the DICT mock).
func DefaultParticipants() Participants {
	return NewParticipants(
		Participant{ISPB: "99000001", Name: "Wallet"},
		Participant{ISPB: "99000002", Name: "Banco Alfa", Simulated: true},
		Participant{ISPB: "99000003", Name: "Banco Beta", Simulated: true},
	)
}

// NewParticipants builds the set from a list.
func NewParticipants(list ...Participant) Participants {
	m := Participants{}
	for _, p := range list {
		m[p.ISPB] = p
	}
	return m
}

// Known reports whether the ISPB is a registered participant.
func (ps Participants) Known(ispb string) bool {
	_, ok := ps[ispb]
	return ok
}

// Simulated reports whether the mock answers for the participant.
func (ps Participants) Simulated(ispb string) bool { return ps[ispb].Simulated }
