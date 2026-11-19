package pix

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	dictTimeout     = 10 * time.Second
	maxResponseSize = 1 << 20
)

// DictClient is the participant side of the DICT API v2. It does not retry: the caller decides, using Retryable and
// APIError.RetryAfter.
type DictClient struct {
	baseURL string
	ispb    string
	http    *http.Client
}

// NewDictClient creates a client for the DICT at baseURL (including /api/v2) acting as the participant ispb.
func NewDictClient(baseURL, ispb string, opts ...DictOption) *DictClient {
	c := &DictClient{baseURL: strings.TrimRight(baseURL, "/"), ispb: ispb, http: &http.Client{Timeout: dictTimeout}}
	for _, o := range opts {
		o(c)
	}
	return c
}

type DictOption func(*DictClient)

func WithHTTPClient(h *http.Client) DictOption { return func(c *DictClient) { c.http = h } }

// GetEntryParams are the values the DICT requires in headers for a lookup.
type GetEntryParams struct {
	Key        string
	PayerID    string // CPF or CNPJ of who is paying
	EndToEndID string // created before the lookup and reused in the pacs.008 (see NewEndToEndID)
}

// GetEntry looks up a key before a payment. ErrBookTransfer means the wallet holds the key itself.
func (c *DictClient) GetEntry(ctx context.Context, p GetEntryParams) (Entry, error) {
	if p.Key == "" {
		return Entry{}, fmt.Errorf("%w: key is required", ErrBadRequest)
	}
	h := http.Header{}
	h.Set("PI-RequestingParticipant", c.ispb)
	h.Set("PI-PayerId", p.PayerID)
	h.Set("PI-EndToEndId", p.EndToEndID)

	var out entryResponseXML
	if err := c.do(ctx, http.MethodGet, "/entries/"+escapeKey(p.Key), h, nil, http.StatusOK, &out); err != nil {
		return Entry{}, err
	}
	return out.Entry.toEntry(), nil
}

// CreateEntry registers a key. For KeyEVP leave Entry.Key empty and the DICT generates it. Repeating a call with the same
// requestID and data returns the same entry.
func (c *DictClient) CreateEntry(ctx context.Context, e Entry, requestID string) (Entry, error) {
	if requestID == "" {
		return Entry{}, fmt.Errorf("%w: request id is required", ErrBadRequest)
	}
	body := createEntryRequestXML{
		Entry:     entryXML{Key: e.Key, KeyType: string(e.KeyType), Account: e.Account.toXML(), Owner: e.Owner.toXML()},
		Reason:    string(ReasonUserRequested),
		RequestID: requestID,
	}
	var out entryResponseXML
	if err := c.do(ctx, http.MethodPost, "/entries", nil, body, http.StatusCreated, &out); err != nil {
		return Entry{}, err
	}
	return out.Entry.toEntry(), nil
}

type UpdateEntryParams struct {
	Key     string
	Account *Account
	Owner   *Owner
	Reason  EntryReason
}

func (c *DictClient) UpdateEntry(ctx context.Context, p UpdateEntryParams) (Entry, error) {
	if p.Key == "" {
		return Entry{}, fmt.Errorf("%w: key is required", ErrBadRequest)
	}
	body := updateEntryRequestXML{Key: p.Key, Reason: string(orDefault(p.Reason))}
	if p.Account != nil {
		a := p.Account.toXML()
		body.Account = &a
	}
	if p.Owner != nil {
		o := p.Owner.toXML()
		body.Owner = &o
	}
	var out entryResponseXML
	if err := c.do(ctx, http.MethodPut, "/entries/"+escapeKey(p.Key), nil, body, http.StatusOK, &out); err != nil {
		return Entry{}, err
	}
	return out.Entry.toEntry(), nil
}

// DeleteEntry removes a key. The reason defaults to USER_REQUESTED.
func (c *DictClient) DeleteEntry(ctx context.Context, key string, reason EntryReason) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrBadRequest)
	}
	body := deleteEntryRequestXML{Key: key, Participant: c.ispb, Reason: string(orDefault(reason))}
	var out deleteEntryResponseXML
	return c.do(ctx, http.MethodPost, "/entries/"+escapeKey(key)+"/delete", nil, body, http.StatusOK, &out)
}

// CheckKeys tells which of 1 to 200 keys have an entry.
func (c *DictClient) CheckKeys(ctx context.Context, keys []string) ([]KeyStatus, error) {
	if len(keys) < 1 || len(keys) > 200 {
		return nil, fmt.Errorf("%w: CheckKeys takes 1 to 200 keys, got %d", ErrBadRequest, len(keys))
	}
	var body checkKeysRequestXML
	body.Keys.Key = keys
	var out checkKeysResponseXML
	if err := c.do(ctx, http.MethodPost, "/keys/check", nil, body, http.StatusOK, &out); err != nil {
		return nil, err
	}
	res := make([]KeyStatus, 0, len(out.Keys.Key))
	for _, k := range out.Keys.Key {
		res = append(res, KeyStatus{Key: k.Key, HasEntry: k.HasEntry})
	}
	return res, nil
}

func orDefault(r EntryReason) EntryReason {
	if r == "" {
		return ReasonUserRequested
	}
	return r
}

// escapeKey escapes a key for the URL path, writing "+" as %2B.
func escapeKey(key string) string {
	return strings.ReplaceAll(url.PathEscape(key), "+", "%2B")
}

// do sends a request and decodes the answer. Any status but wantStatus becomes an *APIError.
func (c *DictClient) do(ctx context.Context, method, path string, extra http.Header, body any, wantStatus int, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := xml.Marshal(body)
		if err != nil {
			return fmt.Errorf("dict: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("dict: build request: %w", err)
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Participant-ISPB", c.ispb)
	if body != nil {
		req.Header.Set("Content-Type", "application/xml")
	}
	for k, v := range extra {
		req.Header[k] = v
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// the DICT may or may not have processed the call
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("%w: reading answer: %w", ErrUnavailable, err)
	}

	if resp.StatusCode != wantStatus {
		return decodeProblem(resp, raw)
	}
	if err := xml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("dict: decode answer: %w", err)
	}
	return nil
}

// decodeProblem turns an error answer into an *APIError, even when the body is not a problem+xml.
func decodeProblem(resp *http.Response, raw []byte) error {
	e := &APIError{Status: resp.StatusCode, Kind: "Unknown", RetryAfter: retryAfter(resp.Header)}
	var p problemXML
	if err := xml.Unmarshal(raw, &p); err != nil || p.Type == "" {
		return e
	}
	e.Kind = p.Type[strings.LastIndex(p.Type, "/")+1:]
	e.Title, e.Detail, e.CorrelationID = p.Title, p.Detail, p.CorrelationID
	for _, v := range p.Violations.Violation {
		e.Violations = append(e.Violations, Violation{Reason: v.Reason, Value: v.Value, Property: v.Property})
	}
	return e
}

func retryAfter(h http.Header) time.Duration {
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}
