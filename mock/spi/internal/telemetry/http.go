package telemetry

import (
	"net/http"
	"strings"
	"unicode"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Handler traces and measures every request but the health check and the long-poll reads of a stream.
func Handler(h http.Handler, service string) http.Handler {
	return otelhttp.NewHandler(h, service,
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/healthz" && !(r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/stream/"))
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + route(r.URL.Path) }))
}

// route replaces the parts of a path that identify something (a key, an ISPB, an id) with {id}, so span names stay few.
func route(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		hasDigit := strings.IndexFunc(p, unicode.IsDigit) >= 0
		if strings.ContainsAny(p, "@+%") || (hasDigit && len(p) > 3) { // "v2" stays, "99000001" does not
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}
