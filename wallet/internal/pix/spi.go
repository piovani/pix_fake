package pix

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const (
	spiTimeout  = 30 * time.Second // longer than the SPI's long poll
	maxMessages = 10
	xmlType     = "application/xml; charset=utf-8"
)

// ErrStreamGone means the stream path is too old: open a new stream with Pull(ctx, "").
var ErrStreamGone = errors.New("spi: stream path too old")

// It does not retry; sending the same message again is safe.
type SPIClient struct {
	baseURL string
	origin  string
	ispb    string
	http    *http.Client
}

func NewSPIClient(baseURL, ispb string, opts ...SPIOption) *SPIClient {
	c := &SPIClient{baseURL: strings.TrimRight(baseURL, "/"), ispb: ispb, http: &http.Client{Timeout: spiTimeout, Transport: tracedTransport()}}
	if u, err := url.Parse(baseURL); err == nil {
		c.origin = u.Scheme + "://" + u.Host
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type SPIOption func(*SPIClient)

func WithSPIHTTPClient(h *http.Client) SPIOption { return func(c *SPIClient) { c.http = h } }

// Message is one message of the stream. A message not acknowledged is delivered again with the same ResourceID.
type Message struct {
	ResourceID string
	Body       []byte
}

// The result of a payment comes through the stream.
func (c *SPIClient) Send(ctx context.Context, bodies ...[]byte) ([]string, error) {
	if len(bodies) < 1 || len(bodies) > maxMessages {
		return nil, fmt.Errorf("%w: send takes 1..%d messages, got %d", ErrBadRequest, maxMessages, len(bodies))
	}
	body, contentType := bodies[0], xmlType
	if len(bodies) > 1 {
		var err error
		if body, contentType, err = multipartBody(bodies); err != nil {
			return nil, err
		}
	}
	resp, raw, err := c.do(ctx, http.MethodPost, c.baseURL+"/in/"+c.ispb+"/msgs", bytes.NewReader(body), contentType, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, decodeSPIError(resp, raw)
	}
	return strings.Split(resp.Header.Get("PI-ResourceId"), ","), nil
}

// Pass "" to open a stream, then the returned next path.
func (c *SPIClient) Pull(ctx context.Context, next string) (msgs []Message, nextPath string, err error) {
	target := c.baseURL + "/out/" + c.ispb + "/stream/start"
	if next != "" {
		target = c.origin + next
	}
	resp, raw, err := c.do(ctx, http.MethodGet, target, nil, "", "multipart/mixed")
	if err != nil {
		return nil, "", err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
	default:
		return nil, "", decodeSPIError(resp, raw)
	}
	nextPath = resp.Header.Get("PI-Pull-Next")
	if nextPath == "" {
		return nil, "", errors.New("spi: answer without PI-Pull-Next")
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, nextPath, nil
	}
	msgs, err = readMessages(resp, raw)
	return msgs, nextPath, err
}

func (c *SPIClient) Ack(ctx context.Context, next string) error {
	if next == "" {
		return fmt.Errorf("%w: ack needs a next path", ErrBadRequest)
	}
	resp, raw, err := c.do(ctx, http.MethodDelete, c.origin+next, nil, "", "")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return decodeSPIError(resp, raw)
	}
	return nil
}

func (c *SPIClient) do(ctx context.Context, method, target string, body io.Reader, contentType, accept string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, nil, fmt.Errorf("spi: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// the SPI may or may not have processed the call
		return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: reading answer: %w", ErrUnavailable, err)
	}
	return resp, raw, nil
}

func multipartBody(bodies [][]byte) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, b := range bodies {
		part, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {xmlType}})
		if err != nil {
			return nil, "", fmt.Errorf("spi: build multipart: %w", err)
		}
		_, _ = part.Write(b)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("spi: build multipart: %w", err)
	}
	return buf.Bytes(), "multipart/mixed; boundary=" + mw.Boundary(), nil
}

func readMessages(resp *http.Response, raw []byte) ([]Message, error) {
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("spi: answer content type: %w", err)
	}
	if mediaType != "multipart/mixed" {
		return []Message{{ResourceID: resp.Header.Get("PI-ResourceId"), Body: raw}}, nil
	}
	var msgs []Message
	mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return msgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("spi: reading multipart answer: %w", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return nil, fmt.Errorf("spi: reading multipart answer: %w", err)
		}
		msgs = append(msgs, Message{ResourceID: part.Header.Get("PI-ResourceId"), Body: body})
	}
}

type SPIError struct {
	Status     int
	Kind       string
	Title      string
	Detail     string
	RetryAfter time.Duration
}

func (e *SPIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("spi: %d %s: %s", e.Status, e.Kind, e.Detail)
	}
	return fmt.Sprintf("spi: %d %s", e.Status, e.Kind)
}

func (e *SPIError) Is(target error) bool {
	switch target {
	case ErrStreamGone:
		return e.Status == http.StatusGone
	case ErrBadRequest:
		return e.Status == http.StatusBadRequest
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrRateLimited:
		return e.Status == http.StatusTooManyRequests
	case ErrUnavailable:
		return e.Status >= 500
	}
	return false
}

func decodeSPIError(resp *http.Response, raw []byte) error {
	e := &SPIError{Status: resp.StatusCode, Kind: "Unknown", RetryAfter: retryAfter(resp.Header)}
	var p problemXML
	if err := xml.Unmarshal(raw, &p); err == nil && p.Type != "" {
		e.Kind = p.Type[strings.LastIndex(p.Type, "/")+1:]
		e.Title, e.Detail = p.Title, p.Detail
	}
	return e
}
