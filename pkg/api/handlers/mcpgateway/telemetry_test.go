package mcpgateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func useSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

func spanAttributes(span sdktrace.ReadOnlySpan) map[attribute.Key]string {
	attrs := make(map[attribute.Key]string)
	for _, attr := range span.Attributes() {
		attrs[attr.Key] = attr.Value.String()
	}
	return attrs
}

func TestMCPOperationSpan(t *testing.T) {
	tests := []struct {
		name            string
		requestBody     string
		contentType     string
		status          int
		responseBody    string
		resolvedTool    string
		blocked         bool
		wantSpanName    string
		wantAttrs       map[attribute.Key]string
		wantErrorType   string
		wantErrorStatus bool
	}{
		{
			name:         "successful tool call with JSON response",
			requestBody:  `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
			contentType:  "application/json",
			status:       http.StatusOK,
			responseBody: `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`,
			wantSpanName: "tools/call echo",
			wantAttrs: map[attribute.Key]string{
				"mcp.method.name":       "tools/call",
				"gen_ai.tool.name":      "echo",
				"gen_ai.operation.name": "execute_tool",
				"jsonrpc.request.id":    "1",
				"mcp.session.id":        "session-1",
				"obot.mcp.hop":          mcpHopGateway,
			},
		},
		{
			name:            "tool error result in SSE response",
			requestBody:     `{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"fail"}}`,
			contentType:     "text/event-stream",
			status:          http.StatusOK,
			responseBody:    "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"a\",\"result\":{\"isError\":true}}\n\n",
			wantSpanName:    "tools/call fail",
			wantErrorType:   "tool_error",
			wantErrorStatus: true,
		},
		{
			name:            "JSON-RPC error response",
			requestBody:     `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"greeting"}}`,
			contentType:     "application/json",
			status:          http.StatusOK,
			responseBody:    `{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"unknown prompt"}}`,
			wantSpanName:    "prompts/get greeting",
			wantAttrs:       map[attribute.Key]string{"gen_ai.prompt.name": "greeting", "rpc.response.status_code": "-32602"},
			wantErrorType:   "-32602",
			wantErrorStatus: true,
		},
		{
			name:            "HTTP error status",
			requestBody:     `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
			contentType:     "text/plain",
			status:          http.StatusBadGateway,
			responseBody:    "upstream unavailable",
			wantSpanName:    "tools/list",
			wantErrorType:   "502",
			wantErrorStatus: true,
		},
		{
			name:         "generic tool call resolved to a component tool",
			requestBody:  `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"call_tool"}}`,
			contentType:  "application/json",
			status:       http.StatusOK,
			responseBody: `{"jsonrpc":"2.0","id":4,"result":{}}`,
			resolvedTool: "github__search",
			wantSpanName: "tools/call github__search",
			wantAttrs:    map[attribute.Key]string{"gen_ai.tool.name": "github__search"},
		},
		{
			name:            "request blocked by a filter",
			requestBody:     `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"delete"}}`,
			contentType:     "application/json",
			status:          http.StatusOK,
			responseBody:    `{"jsonrpc":"2.0","id":5,"result":{}}`,
			blocked:         true,
			wantSpanName:    "tools/call delete",
			wantAttrs:       map[attribute.Key]string{"obot.mcp.blocked": "true"},
			wantErrorType:   "blocked",
			wantErrorStatus: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := useSpanRecorder(t)

			req := httptest.NewRequest(http.MethodPost, "/mcp-connect/server", strings.NewReader(tt.requestBody))
			req.Header.Set(mcpSessionHeader, "session-1")
			op, req := startMCPOperation(req, mcpHopGateway)
			if op == nil {
				t.Fatal("no operation was started")
			}

			body, err := io.ReadAll(req.Body)
			if err != nil || string(body) != tt.requestBody {
				t.Fatalf("request body after peek = %q, %v; want %q", body, err, tt.requestBody)
			}

			op.setResolvedToolName(tt.resolvedTool)
			if tt.blocked {
				op.setBlocked()
			}

			recorderWriter := httptest.NewRecorder()
			w := op.observe(recorderWriter)
			w.Header().Set("Content-Type", tt.contentType)
			w.WriteHeader(tt.status)
			// Split the body to exercise buffering across writes.
			half := len(tt.responseBody) / 2
			_, _ = w.Write([]byte(tt.responseBody[:half]))
			_, _ = w.Write([]byte(tt.responseBody[half:]))
			finishObserved(w)
			op.end(nil)

			if recorderWriter.Body.String() != tt.responseBody {
				t.Fatalf("response body = %q, want %q", recorderWriter.Body.String(), tt.responseBody)
			}

			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("recorded %d spans, want 1", len(spans))
			}
			span := spans[0]
			if span.Name() != tt.wantSpanName {
				t.Fatalf("span name = %q, want %q", span.Name(), tt.wantSpanName)
			}
			attrs := spanAttributes(span)
			for key, want := range tt.wantAttrs {
				if attrs[key] != want {
					t.Errorf("attribute %s = %q, want %q", key, attrs[key], want)
				}
			}
			if attrs["error.type"] != tt.wantErrorType {
				t.Errorf("error.type = %q, want %q", attrs["error.type"], tt.wantErrorType)
			}
			if gotError := span.Status().Code == codes.Error; gotError != tt.wantErrorStatus {
				t.Errorf("span status = %v, want error %v", span.Status(), tt.wantErrorStatus)
			}
		})
	}
}

func TestStartMCPOperationSkipsNonMessages(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
	}{
		{
			name:   "GET stream",
			method: http.MethodGet,
		},
		{
			name:   "client response",
			method: http.MethodPost,
			body:   `{"jsonrpc":"2.0","id":1,"result":{}}`,
		},
		{
			name:   "invalid JSON",
			method: http.MethodPost,
			body:   `{"jsonrpc":`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, "/mcp-connect/server", body)
			op, got := startMCPOperation(req, mcpHopGateway)
			if op != nil {
				t.Fatal("an operation was started")
			}
			if tt.body != "" {
				read, _ := io.ReadAll(got.Body)
				if string(read) != tt.body {
					t.Fatalf("request body after peek = %q, want %q", read, tt.body)
				}
			}
		})
	}
}
