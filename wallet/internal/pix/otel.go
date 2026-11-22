package pix

import (
	"net/http"
	"strings"
	"unicode"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Reading the SPI stream is a long poll every few seconds; tracing it would bury the payments.
func tracedTransport() http.RoundTripper {
	return otelhttp.NewTransport(http.DefaultTransport,
		otelhttp.WithFilter(func(r *http.Request) bool { return !strings.Contains(r.URL.Path, "/stream/") }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + route(r.URL.Path) }))
}

// route replaces the parts of a path that identify something (a key, an ISPB) with {id}, so span names stay few.
func route(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.IndexFunc(p, func(r rune) bool { return unicode.IsDigit(r) || r == '@' || r == '+' || r == '%' }) >= 0 {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}
