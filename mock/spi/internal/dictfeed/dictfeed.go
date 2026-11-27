// Package dictfeed tells the DICT mock of the payments the SPI settles. The real DICT knows every transaction settled
// in the SPI, and the MED takes an infraction report or a refund only on one it knows. The mock's DICT learns them
// through its control API (POST /mock/transactions), and this is the SPI's side of it.
//
// The settled payments not sent yet are an outbox (pix.NotificationStore): a payment is sent at least once, the oldest
// first, and the DICT replaces a transaction it already has, so a repeat does no harm.
package dictfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"pixfake/mock/spi/internal/pix"
)

// window is how far back the feed looks: past 90 days a payment can no longer be returned, so the MED has no use for
// it. It matters when the feed is turned on with payments settled long before.
const window = 90 * 24 * time.Hour

const batch = 100

var (
	meter    = otel.Meter("pixfake/mock/spi/dictfeed")
	sent, _  = meter.Int64Counter("spi.dict.notifications", metric.WithDescription("settled payments sent to the DICT"))
	maxDelay = time.Minute
)

type Feed struct {
	url    string
	store  pix.NotificationStore
	client *http.Client
	log    *slog.Logger
	now    func() time.Time
}

// New creates the feed to the DICT mock at dictURL (its base URL, as http://dict:8080).
func New(dictURL string, store pix.NotificationStore, log *slog.Logger) *Feed {
	return &Feed{
		url: strings.TrimRight(dictURL, "/") + "/mock/transactions", store: store,
		client: &http.Client{Timeout: 10 * time.Second}, log: log, now: time.Now,
	}
}

// SetClock replaces the clock, for tests.
func (f *Feed) SetClock(now func() time.Time) { f.now = now }

// permanentError is a transaction the DICT refused as invalid: sending it again would not change the answer.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// Flush sends what is in the outbox and returns how many the DICT took. It stops at the first failure that may pass
// (the DICT down or not answering), leaving the rest for the next call. A payment the DICT refuses as invalid is
// logged and taken out of the outbox.
func (f *Feed) Flush(ctx context.Context) (int, error) {
	n := 0
	for {
		pending, err := f.store.PendingNotifications(ctx, f.now().Add(-window), batch)
		if err != nil {
			return n, err
		}
		for _, p := range pending {
			err := f.send(ctx, p)
			if perm, ok := err.(*permanentError); ok {
				f.log.ErrorContext(ctx, "the DICT refused a settled payment", "end_to_end_id", p.ID, "error", perm.msg)
				count(ctx, "refused")
			} else if err != nil {
				count(ctx, "failed")
				return n, err
			} else {
				count(ctx, "sent")
				n++
			}
			if err := f.store.MarkNotified(ctx, p.ID, f.now()); err != nil {
				return n, err
			}
		}
		if len(pending) < batch {
			return n, nil
		}
	}
}

// Run flushes every interval until ctx is done. While the DICT fails it waits longer each time, up to a minute.
func (f *Feed) Run(ctx context.Context, every time.Duration) {
	delay := every
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if _, err := f.Flush(ctx); err != nil {
			if !failing {
				f.log.WarnContext(ctx, "cannot tell the DICT of settled payments; trying again", "url", f.url, "error", err)
			}
			failing, delay = true, min(delay*2, maxDelay)
			continue
		}
		if failing {
			f.log.InfoContext(ctx, "the DICT is taking settled payments again")
		}
		failing, delay = false, every
	}
}

type party struct {
	Participant string `json:"participant"`
	TaxID       string `json:"tax_id"`
	Key         string `json:"key,omitempty"`
}

type transaction struct {
	EndToEndID string    `json:"end_to_end_id"`
	Amount     string    `json:"amount"`
	SettledAt  time.Time `json:"settled_at"`
	Debtor     party     `json:"debtor"`
	Creditor   party     `json:"creditor"`
}

func (f *Feed) send(ctx context.Context, p pix.Payment) error {
	body, err := json.Marshal(transaction{
		EndToEndID: p.ID, Amount: fmt.Sprintf("%d.%02d", p.AmountCents/100, p.AmountCents%100), SettledAt: p.SettledAt.UTC(),
		Debtor:   party{Participant: p.PayerISPB, TaxID: p.PayerTaxID},
		Creditor: party{Participant: p.PayeeISPB, TaxID: p.PayeeTaxID, Key: p.PayeeKey},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusBadRequest:
		return &permanentError{msg: fmt.Sprintf("%d: %s", resp.StatusCode, bytes.TrimSpace(msg))}
	default:
		return fmt.Errorf("POST %s: %d: %s", f.url, resp.StatusCode, bytes.TrimSpace(msg))
	}
}

func count(ctx context.Context, outcome string) {
	sent.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}
