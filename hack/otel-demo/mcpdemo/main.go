// Command mcpdemo runs a small, OpenTelemetry-instrumented MCP server for demos.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var tracer trace.Tracer

// work records a child span that takes roughly d.
func work(ctx context.Context, name string, d time.Duration, attrs ...attribute.KeyValue) {
	_, span := tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	time.Sleep(d)
	span.End()
}

// traced returns the tool handler's context with the caller's trace. The SDK runs
// handlers on the session's context, so the trace comes from the request headers.
func traced(ctx context.Context, req *mcp.CallToolRequest) context.Context {
	if req.Extra != nil && req.Extra.Header != nil {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(req.Extra.Header))
	}
	return ctx
}

func jitter(min, max int) time.Duration {
	return time.Duration(min+rand.IntN(max-min)) * time.Millisecond
}

type cityArgs struct {
	City string `json:"city" jsonschema:"the city to look up"`
}

type queryArgs struct {
	Query string `json:"query" jsonschema:"text to search for"`
}

type noteArgs struct {
	Text string `json:"text" jsonschema:"the note to save"`
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func weatherServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "weather", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_forecast", Description: "Get the forecast for a city"},
		func(ctx context.Context, req *mcp.CallToolRequest, args cityArgs) (*mcp.CallToolResult, any, error) {
			ctx = traced(ctx, req)
			city := attribute.String("city", args.City)
			work(ctx, "geocode city", jitter(20, 80), city)
			if strings.EqualFold(args.City, "atlantis") {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unknown city: " + args.City}}}, nil, nil
			}
			work(ctx, "query forecast model", jitter(100, 600), city)
			return text(fmt.Sprintf("%s: %d°F, partly cloudy", args.City, 50+rand.IntN(40))), nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_alerts", Description: "Get active weather alerts for a city"},
		func(ctx context.Context, req *mcp.CallToolRequest, args cityArgs) (*mcp.CallToolResult, any, error) {
			ctx = traced(ctx, req)
			work(ctx, "fetch alerts feed", jitter(30, 150), attribute.String("city", args.City))
			if rand.IntN(5) == 0 {
				return nil, nil, errors.New("alerts feed timed out")
			}
			return text("No active alerts for " + args.City), nil, nil
		})
	return server
}

func notesServer() *mcp.Server {
	var (
		mu    sync.Mutex
		notes = []string{"buy milk", "call the plumber", "renew passport", "ship the otel change"}
	)
	server := mcp.NewServer(&mcp.Implementation{Name: "notes", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "search_notes", Description: "Search saved notes"},
		func(ctx context.Context, req *mcp.CallToolRequest, args queryArgs) (*mcp.CallToolResult, any, error) {
			ctx = traced(ctx, req)
			work(ctx, "SELECT notes", jitter(5, 60), attribute.String("db.system.name", "sqlite"))
			mu.Lock()
			defer mu.Unlock()
			var found []string
			for _, note := range notes {
				if strings.Contains(note, strings.ToLower(args.Query)) {
					found = append(found, note)
				}
			}
			return text(fmt.Sprintf("found %d: %s", len(found), strings.Join(found, "; "))), nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "add_note", Description: "Save a note"},
		func(ctx context.Context, req *mcp.CallToolRequest, args noteArgs) (*mcp.CallToolResult, any, error) {
			ctx = traced(ctx, req)
			work(ctx, "INSERT note", jitter(10, 90), attribute.String("db.system.name", "sqlite"))
			mu.Lock()
			notes = append(notes, args.Text)
			mu.Unlock()
			return text("saved"), nil, nil
		})
	return server
}

func main() {
	kind := flag.String("server", "weather", "weather or notes")
	addr := flag.String("addr", "127.0.0.1:9123", "listen address")
	flag.Parse()

	ctx := context.Background()
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		log.Fatal(err)
	}
	res, _ := resource.New(ctx, resource.WithFromEnv(), resource.WithAttributes(attribute.String("service.name", *kind+"-mcp")))
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	defer provider.Shutdown(ctx)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tracer = otel.Tracer("mcpdemo")

	server := weatherServer()
	if *kind == "notes" {
		server = notesServer()
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	log.Printf("%s MCP server on http://%s/mcp", *kind, *addr)
	if err := http.ListenAndServe(*addr, otelhttp.NewHandler(handler, *kind+"-mcp")); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
