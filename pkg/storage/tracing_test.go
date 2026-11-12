package storage

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStorageSpanName(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{
			name:   "get named resource",
			method: http.MethodGet,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/vmcps/vmcp1rjcp5",
			want:   "storage get vmcps",
		},
		{
			name:   "list namespaced resources",
			method: http.MethodGet,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/mcpservers",
			want:   "storage list mcpservers",
		},
		{
			name:   "list across namespaces",
			method: http.MethodGet,
			path:   "/apis/obot.obot.ai/v1/authproviders",
			want:   "storage list authproviders",
		},
		{
			name:   "watch",
			method: http.MethodGet,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/mcpservers?watch=true",
			want:   "storage watch mcpservers",
		},
		{
			name:   "update status subresource",
			method: http.MethodPut,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/mcpclientsessions/s1/status",
			want:   "storage update mcpclientsessions/status",
		},
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/mcpclientsessions",
			want:   "storage create mcpclientsessions",
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/mcpclientsessions/s1",
			want:   "storage delete mcpclientsessions",
		},
		{
			name:   "delete collection",
			method: http.MethodDelete,
			path:   "/apis/obot.obot.ai/v1/namespaces/default/mcpclientsessions",
			want:   "storage deletecollection mcpclientsessions",
		},
		{
			name:   "core group namespace",
			method: http.MethodGet,
			path:   "/api/v1/namespaces/default",
			want:   "storage get namespaces",
		},
		{
			name:   "non-resource path",
			method: http.MethodGet,
			path:   "/version",
			want:   "storage get /version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if got := storageSpanName(req); got != tt.want {
				t.Fatalf("storageSpanName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStorageTransportSpans(t *testing.T) {
	tests := []struct {
		name          string
		traceRequests bool
		wantSpans     []string
	}{
		{
			name:          "spans are off by default",
			traceRequests: false,
		},
		{
			name:          "spans are named by verb and resource when enabled",
			traceRequests: true,
			wantSpans:     []string{"storage get vmcps"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
			t.Cleanup(func() { otel.SetTracerProvider(previous) })

			client := &http.Client{Transport: newStorageTransport(http.DefaultTransport, tt.traceRequests)}
			resp, err := client.Get(server.URL + "/apis/obot.obot.ai/v1/namespaces/default/vmcps/v1")
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()

			var got []string
			for _, span := range recorder.Ended() {
				got = append(got, span.Name())
			}
			if len(got) != len(tt.wantSpans) {
				t.Fatalf("spans = %v, want %v", got, tt.wantSpans)
			}
			for i := range got {
				if got[i] != tt.wantSpans[i] {
					t.Fatalf("spans = %v, want %v", got, tt.wantSpans)
				}
			}
		})
	}
}
