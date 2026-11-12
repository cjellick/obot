package mcpgateway

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/obot-platform/mmmcp/component"
	"github.com/obot-platform/obot/pkg/mcp"
	"github.com/obot-platform/obot/pkg/mcp/auditlogs"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apiserver/pkg/authentication/user"
)

const (
	instrumentationName = "github.com/obot-platform/obot/pkg/api/handlers/mcpgateway"

	// maxObservedResponseSize bounds the JSON response bytes buffered to find a JSON-RPC error.
	maxObservedResponseSize = 1 << 20

	// Hops of the MCP gateway. A client's request reaches the gateway hop. For a vMCP,
	// the composite hop (traced by mmmcp) fans the request out to one component hop
	// for each MCP server it calls.
	mcpHopGateway   = "gateway"
	mcpHopComponent = "component"

	observedUnknown observedResponse = 0
	observedJSON    observedResponse = 1
	observedSSE     observedResponse = 2

	// Attribute keys that the MCP semantic conventions do not define are prefixed with obot.
	attrJSONRPCRequestID   = attribute.Key("jsonrpc.request.id")
	attrMCPProtocolVersion = attribute.Key("mcp.protocol.version")
	attrNetworkTransport   = attribute.Key("network.transport")
	attrNetworkProtocol    = attribute.Key("network.protocol.name")
	attrUserID             = attribute.Key("user.id")
	attrHop                = attribute.Key("obot.mcp.hop")
	attrServerID           = attribute.Key("obot.mcp.server.id")
	attrServerName         = attribute.Key("obot.mcp.server.name")
	attrRuntime            = attribute.Key("obot.mcp.runtime")
	attrClientName         = attribute.Key("obot.mcp.client.name")
	attrClientVersion      = attribute.Key("obot.mcp.client.version")
	attrBlocked            = attribute.Key("obot.mcp.blocked")
	attrFilterName         = attribute.Key("obot.mcp.filter.name")
	attrFilterType         = attribute.Key("obot.mcp.filter.type")
	attrFilterDirection    = attribute.Key("obot.mcp.filter.direction")
	attrFilterOutcome      = attribute.Key("obot.mcp.filter.outcome")
)

var (
	// operationDuration is the mcp.server.operation.duration histogram from the MCP
	// semantic conventions, using their recommended bucket boundaries.
	operationDuration = sync.OnceValue(func() metric.Float64Histogram {
		histogram, err := otel.Meter(instrumentationName).Float64Histogram("mcp.server.operation.duration",
			metric.WithDescription("Duration of MCP requests handled by the Obot MCP gateway."),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300),
		)
		if err != nil {
			otel.Handle(err)
		}
		return histogram
	})
)

// mcpOperation traces one JSON-RPC request or notification through a gateway hop
// and records its duration. A nil operation is valid and records nothing.
type mcpOperation struct {
	span      trace.Span
	start     time.Time
	method    string
	requestID string

	// Low-cardinality attributes shared by the span and the duration metric.
	metricAttrs map[attribute.Key]attribute.KeyValue

	errorType string
	once      sync.Once
}

type observedResponse int

// mcpResponseObserver passes a response through while finding the JSON-RPC
// response to the traced request, in either a JSON body or an SSE stream.
type mcpResponseObserver struct {
	http.ResponseWriter
	op          *mcpOperation
	wroteHeader bool
	kind        observedResponse
	body        bytes.Buffer
	truncated   bool
	line        []byte
	data        []string
}

func tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// startMCPOperation starts the span for the MCP message in req, if it carries one,
// and returns the request with the span in its context. Only POST requests carry
// client messages; GET streams and session deletes are traced at the HTTP level.
func startMCPOperation(req *http.Request, hop string) (*mcpOperation, *http.Request) {
	if req.Method != http.MethodPost || req.Body == nil {
		return nil, req
	}

	body, ok := peekMCPRequest(req)
	if !ok {
		return nil, req
	}

	var msg mcp.Message
	if decodeMCPHookMessage(body, &msg) != nil || msg.Method == "" {
		return nil, req
	}

	op := &mcpOperation{
		start:       time.Now(),
		method:      msg.Method,
		requestID:   mcp.MessageIDString(msg.ID),
		metricAttrs: map[attribute.Key]attribute.KeyValue{},
	}

	identifier := mcpCallIdentifier(msg)
	var name, uri string
	if msg.Method == "resources/read" {
		uri = identifier
	} else {
		name = identifier
	}

	attrs := component.OperationAttributes(msg.Method, name, uri)
	for _, attr := range attrs {
		if attr.Key != component.AttrMCPResourceURI {
			op.metricAttrs[attr.Key] = attr
		}
	}
	op.metricAttrs[attrHop] = attrHop.String(hop)
	attrs = append(attrs,
		attrHop.String(hop),
		attrNetworkTransport.String("tcp"),
		attrNetworkProtocol.String("http"),
	)
	if op.requestID != "" {
		attrs = append(attrs, attrJSONRPCRequestID.String(op.requestID))
	}
	if sessionID := mcpSessionID(req.Header, req.URL); sessionID != "" {
		attrs = append(attrs, component.AttrMCPSessionID.String(sessionID))
	}
	if version := req.Header.Get("Mcp-Protocol-Version"); version != "" {
		attrs = append(attrs, attrMCPProtocolVersion.String(version))
	}
	var client auditlogs.MCPAuditLog
	populateMCPClientInfo(&client, msg)
	if client.ClientName != "" {
		attrs = append(attrs, attrClientName.String(client.ClientName))
	}
	if client.ClientVersion != "" {
		attrs = append(attrs, attrClientVersion.String(client.ClientVersion))
	}

	ctx, span := tracer().Start(req.Context(), component.SpanName(msg.Method, name),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
	op.span = span

	return op, req.WithContext(ctx)
}

// setServer records the MCP server that the hop proxies to.
func (o *mcpOperation) setServer(mcpID string, serverConfig mcp.ServerConfig) {
	if o == nil {
		return
	}
	serverName := cmp.Or(serverConfig.MCPServerDisplayName, serverConfig.MCPServerName, mcpID)
	o.metricAttrs[attrServerName] = attrServerName.String(serverName)
	o.metricAttrs[attrRuntime] = attrRuntime.String(string(serverConfig.Runtime))
	o.span.SetAttributes(
		attrServerID.String(mcpID),
		attrServerName.String(serverName),
		attrRuntime.String(string(serverConfig.Runtime)),
	)
}

func (o *mcpOperation) setUser(caller user.Info) {
	if o == nil || caller == nil || caller.GetUID() == "" {
		return
	}
	o.span.SetAttributes(attrUserID.String(caller.GetUID()))
}

// setResolvedToolName records the tool that a vMCP's generic tool call resolved to.
func (o *mcpOperation) setResolvedToolName(name string) {
	if o == nil || name == "" || o.method != "tools/call" {
		return
	}
	attr := component.AttrGenAIToolName.String(name)
	o.metricAttrs[attr.Key] = attr
	o.span.SetAttributes(attr)
	o.span.SetName(component.SpanName(o.method, name))
}

func (o *mcpOperation) setBlocked() {
	if o == nil {
		return
	}
	o.span.SetAttributes(attrBlocked.Bool(true))
	o.setErrorType("blocked")
}

func (o *mcpOperation) setErrorType(errorType string) {
	if o != nil && o.errorType == "" {
		o.errorType = errorType
	}
}

// observeMessage records the outcome carried by a JSON-RPC response to this operation.
func (o *mcpOperation) observeMessage(data []byte) {
	if o == nil || o.requestID == "" {
		return
	}

	var msg mcp.Message
	if json.Unmarshal(data, &msg) != nil || msg.Method != "" || mcp.MessageIDString(msg.ID) != o.requestID {
		return
	}

	if msg.Error != nil {
		code := strconv.Itoa(msg.Error.Code)
		o.span.SetAttributes(component.AttrRPCStatusCode.String(code))
		o.setErrorType(code)
		if msg.Error.Message != "" {
			o.span.SetStatus(codes.Error, msg.Error.Message)
		}
		return
	}

	if o.method == "tools/call" {
		var result struct {
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(msg.Result, &result) == nil && result.IsError {
			o.setErrorType("tool_error")
		}
	}
}

// end records the operation's outcome and duration. err is an error the hop
// returned instead of writing a response.
func (o *mcpOperation) end(err error) {
	if o == nil {
		return
	}
	o.once.Do(func() {
		if err != nil {
			o.span.RecordError(err)
			o.setErrorType("_OTHER")
			o.span.SetStatus(codes.Error, err.Error())
		}
		metricAttrs := make([]attribute.KeyValue, 0, len(o.metricAttrs)+1)
		for _, attr := range o.metricAttrs {
			metricAttrs = append(metricAttrs, attr)
		}
		if o.errorType != "" {
			attr := component.AttrErrorType.String(o.errorType)
			metricAttrs = append(metricAttrs, attr)
			o.span.SetAttributes(attr)
			if err == nil {
				o.span.SetStatus(codes.Error, o.errorType)
			}
		}
		if histogram := operationDuration(); histogram != nil {
			histogram.Record(context.Background(), time.Since(o.start).Seconds(), metric.WithAttributes(metricAttrs...))
		}
		o.span.End()
	})
}

// observe returns a writer that records the outcome of the response written to w.
func (o *mcpOperation) observe(w http.ResponseWriter) http.ResponseWriter {
	if o == nil {
		return w
	}
	return &mcpResponseObserver{ResponseWriter: w, op: o}
}

func (o *mcpResponseObserver) WriteHeader(status int) {
	if !o.wroteHeader {
		o.wroteHeader = true
		if status >= http.StatusBadRequest {
			o.op.setErrorType(strconv.Itoa(status))
		}
		if o.Header().Get("Content-Encoding") == "" {
			mediaType, _, _ := mime.ParseMediaType(o.Header().Get("Content-Type"))
			switch mediaType {
			case "application/json":
				o.kind = observedJSON
			case "text/event-stream":
				o.kind = observedSSE
			}
		}
	}
	o.ResponseWriter.WriteHeader(status)
}

func (o *mcpResponseObserver) Write(p []byte) (int, error) {
	if !o.wroteHeader {
		o.WriteHeader(http.StatusOK)
	}
	switch o.kind {
	case observedJSON:
		if !o.truncated && o.body.Len()+len(p) <= maxObservedResponseSize {
			o.body.Write(p)
		} else {
			o.truncated = true
			o.body.Reset()
		}
	case observedSSE:
		o.consumeSSE(p)
	}
	return o.ResponseWriter.Write(p)
}

func (o *mcpResponseObserver) consumeSSE(p []byte) {
	o.line = append(o.line, p...)
	for {
		index := bytes.IndexByte(o.line, '\n')
		if index < 0 {
			if len(o.line) > maxObservedResponseSize {
				o.line, o.data = nil, nil
			}
			return
		}
		line := strings.TrimSuffix(string(o.line[:index]), "\r")
		o.line = o.line[index+1:]
		if line == "" {
			if len(o.data) > 0 {
				o.op.observeMessage([]byte(strings.Join(o.data, "\n")))
			}
			o.data = nil
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			o.data = append(o.data, strings.TrimPrefix(value, " "))
		}
	}
}

// finish evaluates a buffered JSON response once the handler has returned.
func (o *mcpResponseObserver) finish() {
	if o.kind == observedJSON && !o.truncated {
		o.op.observeMessage(o.body.Bytes())
	}
}

func (o *mcpResponseObserver) Flush() {
	_ = http.NewResponseController(o.ResponseWriter).Flush()
}

func (o *mcpResponseObserver) Unwrap() http.ResponseWriter {
	return o.ResponseWriter
}

// finishObserved evaluates the response written through w, if w observes one.
func finishObserved(w http.ResponseWriter) {
	if observer, ok := w.(*mcpResponseObserver); ok {
		observer.finish()
	}
}

// peekMCPRequest returns the decoded request body for inspection and leaves the
// body to be read again from the start. It reports false for a body too large to inspect.
func peekMCPRequest(req *http.Request) ([]byte, bool) {
	original := req.Body
	raw, err := io.ReadAll(io.LimitReader(original, maxMCPProxyHookBodySize+1))
	req.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), original), original}
	if err != nil || len(raw) > maxMCPProxyHookBodySize {
		return nil, false
	}

	if !strings.EqualFold(strings.TrimSpace(req.Header.Get("Content-Encoding")), "gzip") {
		return raw, true
	}
	decoded, _, err := decodeMCPHookBody(io.NopCloser(bytes.NewReader(raw)), "gzip")
	if err != nil {
		return nil, false
	}
	defer decoded.Close()
	body, err := readMCPHookBody(decoded)
	return body, err == nil
}

// tracedTransport records a client span for each request to an upstream MCP
// server and propagates the trace context to it.
func tracedTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Host
		}),
	)
}

// TracedHTTPClient returns an HTTP client for the composite's component calls
// that records client spans and propagates the trace context.
func TracedHTTPClient() *http.Client {
	return &http.Client{Transport: tracedTransport(http.DefaultTransport)}
}

// injectTraceContext writes the current span's context into the request headers.
// The composite reads it from there: the SDK runs MCP method handlers on the
// session's context, not the request's.
func injectTraceContext(req *http.Request) {
	otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
}

// startFilterSpan starts a span for one hook or guardrail filter.
func startFilterSpan(ctx context.Context, name, filterType, direction, method string) (context.Context, trace.Span) {
	return tracer().Start(ctx, "filter "+name, trace.WithAttributes(
		attrFilterName.String(name),
		attrFilterType.String(filterType),
		attrFilterDirection.String(direction),
		component.AttrMCPMethodName.String(method),
	))
}

func endFilterSpan(span trace.Span, outcome string, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		outcome = "error"
	}
	span.SetAttributes(attrFilterOutcome.String(outcome))
	span.End()
}
