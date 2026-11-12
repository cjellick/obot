package storage

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace/noop"
)

// newStorageTransport instruments requests to the storage API. The requests
// always record HTTP client metrics. They record spans only when traceRequests
// is set: one request to Obot can make dozens of storage requests, which bury
// the rest of its trace.
func newStorageTransport(base http.RoundTripper, traceRequests bool) http.RoundTripper {
	opts := []otelhttp.Option{
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return storageSpanName(r)
		}),
	}
	if !traceRequests {
		opts = append(opts, otelhttp.WithTracerProvider(noop.NewTracerProvider()))
	}
	return otelhttp.NewTransport(base, opts...)
}

// storageSpanName names a storage API request by its verb and resource, for
// example "storage get vmcps" or "storage update mcpclientsessions/status".
func storageSpanName(r *http.Request) string {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(segments) >= 2 && segments[0] == "api":
		// Core group: /api/{version}/...
		segments = segments[2:]
	case len(segments) >= 3 && segments[0] == "apis":
		// Named group: /apis/{group}/{version}/...
		segments = segments[3:]
	default:
		return "storage " + strings.ToLower(r.Method) + " " + r.URL.Path
	}
	if len(segments) >= 2 && segments[0] == "namespaces" && len(segments) > 2 {
		segments = segments[2:]
	}
	if len(segments) == 0 {
		return "storage " + strings.ToLower(r.Method)
	}

	resource := segments[0]
	named := len(segments) >= 2
	if len(segments) >= 3 {
		resource += "/" + segments[2]
	}

	var verb string
	switch r.Method {
	case http.MethodGet:
		switch {
		case r.URL.Query().Get("watch") == "true" || r.URL.Query().Get("watch") == "1":
			verb = "watch"
		case named:
			verb = "get"
		default:
			verb = "list"
		}
	case http.MethodPost:
		verb = "create"
	case http.MethodPut:
		verb = "update"
	case http.MethodPatch:
		verb = "patch"
	case http.MethodDelete:
		verb = "delete"
		if !named {
			verb = "deletecollection"
		}
	default:
		verb = strings.ToLower(r.Method)
	}
	return "storage " + verb + " " + resource
}
