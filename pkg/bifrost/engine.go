// Package bifrost embeds the Bifrost AI gateway (github.com/maximhq/bifrost) inside the
// Obot process and exposes it to the rest of Obot as an http.Handler.
//
// Bifrost runs its full HTTP transport (inference routes, governance, logging, telemetry,
// routing and caching plugins) in-process. It never listens on a network socket: it is
// served over an in-memory listener, so the only way to reach it is through Obot, which
// authenticates callers and applies its own policies before forwarding requests.
package bifrost

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bifrostserver "github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp/fasthttputil"
)

// Version is reported to Bifrost as its runtime version.
const Version = "obot-embedded"

// Header names Obot uses when forwarding requests to Bifrost. Headers prefixed with
// x-bf-lh- are captured by Bifrost's logging plugin into each log entry's metadata, and
// x-bf-dim- headers become dimensions that the analytics endpoints can group by.
const (
	HeaderVirtualKey   = "x-bf-vk"
	HeaderCacheKey     = "x-bf-cache-key"
	HeaderLogUserID    = "x-bf-lh-obot-user-id"
	HeaderLogUserName  = "x-bf-lh-obot-user-name"
	HeaderLogModelID   = "x-bf-lh-obot-model"
	HeaderDimUser      = "x-bf-dim-user"
	HeaderDimObotModel = "x-bf-dim-obot-model"
)

// Options configures the embedded engine.
type Options struct {
	// DataDir holds the generated config.json and, when no Postgres DSN is supplied, the
	// SQLite config and log stores.
	DataDir string
	// PostgresDSN, when set, is used for Bifrost's config and log stores instead of SQLite.
	// It must point to a database (or schema via search_path) that is not shared with Obot,
	// because Bifrost's table names overlap with Obot's.
	PostgresDSN string
	// EncryptionKey encrypts provider keys at rest. When empty a key is generated and
	// persisted in DataDir.
	EncryptionKey string
	// LogRetentionDays controls how long Bifrost keeps request logs.
	LogRetentionDays int
	// LogLevel is the Bifrost logger level (debug, info, warn, error).
	LogLevel string
	// CacheRedisAddr is the address of a Redis (or Valkey) server used as the vector store
	// for response caching. Caching is unavailable without one.
	CacheRedisAddr string
	// CacheRedisPassword authenticates to CacheRedisAddr.
	CacheRedisPassword string
}

// Engine is a running, embedded Bifrost gateway.
type Engine struct {
	server   *bifrostserver.BifrostHTTPServer
	listener *fasthttputil.InmemoryListener
	client   *http.Client
	proxy    *httputil.ReverseProxy
	baseURL  *url.URL

	ready     chan struct{}
	startErr  error
	startOnce sync.Once
}

// internalHost is the synthetic host used for requests to the embedded server. It is never
// resolved; the transport dials the in-memory listener directly.
const internalHost = "bifrost.internal"

// New creates an engine. Call Start to boot it.
func New() *Engine {
	ln := fasthttputil.NewInmemoryListener()
	ln.SetLocalAddr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80})

	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return ln.DialWithLocalAddr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1})
		},
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
		// Streaming responses must not be buffered or transparently decompressed.
		DisableCompression: true,
		ForceAttemptHTTP2:  false,
	}

	baseURL := &url.URL{Scheme: "http", Host: internalHost}
	e := &Engine{
		listener: ln,
		client:   &http.Client{Transport: transport},
		baseURL:  baseURL,
		ready:    make(chan struct{}),
	}
	e.proxy = &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = baseURL.Scheme
			r.Out.URL.Host = baseURL.Host
			r.Out.Host = baseURL.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			slog.Warn("bifrost proxy error", "error", err)
			http.Error(w, "LLM gateway unavailable: "+err.Error(), http.StatusBadGateway)
		},
	}
	return e
}

// Start boots Bifrost in the background. It returns once bootstrap completes or fails.
func (e *Engine) Start(ctx context.Context, opts Options) error {
	e.startOnce.Do(func() {
		defer close(e.ready)
		e.startErr = e.start(ctx, opts)
		if e.startErr != nil {
			slog.Error("failed to start embedded bifrost LLM gateway", "error", e.startErr)
		}
	})
	return e.startErr
}

func (e *Engine) start(ctx context.Context, opts Options) error {
	if opts.DataDir == "" {
		return errors.New("bifrost data dir is required")
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return fmt.Errorf("failed to create bifrost data dir: %w", err)
	}

	encryptionKey, err := resolveEncryptionKey(opts)
	if err != nil {
		return err
	}

	cfg, err := buildConfigFile(opts, encryptionKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opts.DataDir, "config.json"), cfg, 0o600); err != nil {
		return fmt.Errorf("failed to write bifrost config: %w", err)
	}

	level := schemas.LogLevel(strings.ToLower(opts.LogLevel))
	if level == "" {
		level = schemas.LogLevelWarn
	}
	logger := bifrostcore.NewDefaultLogger(level)
	logger.SetOutputType(schemas.LoggerOutputTypeJSON)
	lib.SetLogger(logger)
	bifrostserver.SetLogger(logger)
	handlers.SetLogger(logger)

	server := bifrostserver.NewBifrostHTTPServer(Version, embed.FS{})
	server.AppDir = opts.DataDir
	server.Host = "127.0.0.1"
	server.Port = "0"
	server.LogLevel = string(level)
	server.LogOutputStyle = string(schemas.LoggerOutputTypeJSON)

	start := time.Now()
	if err := server.Bootstrap(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap bifrost: %w", err)
	}
	e.server = server

	go func() {
		if err := server.Server.Serve(e.listener); err != nil && !errors.Is(err, fasthttputil.ErrInmemoryListenerClosed) {
			slog.Error("embedded bifrost server stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		e.Shutdown()
	}()

	slog.Info("embedded bifrost LLM gateway started", "dataDir", opts.DataDir, "duration", time.Since(start))
	return nil
}

// Shutdown stops the embedded server.
func (e *Engine) Shutdown() {
	if e.server == nil {
		return
	}
	if e.server.Server != nil {
		_ = e.server.Server.Shutdown()
	}
	_ = e.listener.Close()
	if e.server.Client != nil {
		e.server.Client.Shutdown()
	}
}

// Wait blocks until the engine has finished starting (successfully or not).
func (e *Engine) Wait(ctx context.Context) error {
	select {
	case <-e.ready:
		return e.startErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ready reports whether the engine has started successfully.
func (e *Engine) Ready() bool {
	select {
	case <-e.ready:
		return e.startErr == nil
	default:
		return false
	}
}

// Err returns the startup error, if any.
func (e *Engine) Err() error {
	select {
	case <-e.ready:
		return e.startErr
	default:
		return nil
	}
}

// ServeHTTP forwards the request to the embedded Bifrost server unchanged. Callers are
// responsible for authenticating the request and rewriting the path to a Bifrost route.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !e.Ready() {
		msg := "LLM gateway is starting"
		if err := e.Err(); err != nil {
			msg = "LLM gateway failed to start: " + err.Error()
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	e.proxy.ServeHTTP(w, r)
}

// Transport returns a RoundTripper that sends requests to the embedded server regardless of
// the request's host.
func (e *Engine) Transport() http.RoundTripper {
	return e.client.Transport
}

// BaseURL is the URL requests to the embedded server should be addressed to.
func (e *Engine) BaseURL() url.URL {
	return *e.baseURL
}

// Client exposes the embedded Bifrost core client for direct, in-process calls.
func (e *Engine) Client() *bifrostcore.Bifrost {
	if !e.Ready() {
		return nil
	}
	return e.server.Client
}

// Do sends a request to the embedded server's API and decodes a JSON response into out.
func (e *Engine) Do(ctx context.Context, method, path string, body any, out any) error {
	if !e.Ready() {
		if err := e.Err(); err != nil {
			return fmt.Errorf("LLM gateway failed to start: %w", err)
		}
		return errors.New("LLM gateway is starting")
	}

	var reader *strings.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(data))
	} else {
		reader = strings.NewReader("")
	}

	u := e.BaseURL()
	u.Path = path
	if p, q, ok := strings.Cut(path, "?"); ok {
		u.Path = p
		u.RawQuery = q
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		msg := errBody.Error.Message
		if msg == "" {
			msg = errBody.Message
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// APIError is returned by Do for non-2xx responses from Bifrost.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bifrost returned %d: %s", e.StatusCode, e.Message)
}

func resolveEncryptionKey(opts Options) (string, error) {
	if opts.EncryptionKey != "" {
		return opts.EncryptionKey, nil
	}
	keyFile := filepath.Join(opts.DataDir, "encryption.key")
	if data, err := os.ReadFile(keyFile); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		return strings.TrimSpace(string(data)), nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read bifrost encryption key: %w", err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := hex.EncodeToString(buf)
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		return "", fmt.Errorf("failed to persist bifrost encryption key: %w", err)
	}
	return key, nil
}

func buildConfigFile(opts Options, encryptionKey string) ([]byte, error) {
	retention := opts.LogRetentionDays
	if retention <= 0 {
		retention = 30
	}

	storeConfig := func(file string) (map[string]any, error) {
		if opts.PostgresDSN == "" {
			return map[string]any{
				"enabled": true,
				"type":    "sqlite",
				"config":  map[string]any{"path": filepath.Join(opts.DataDir, file)},
			}, nil
		}
		pg, err := postgresConfigFromDSN(opts.PostgresDSN)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"enabled": true,
			"type":    "postgres",
			"config":  pg,
		}, nil
	}

	configStore, err := storeConfig("config.db")
	if err != nil {
		return nil, err
	}
	logsStore, err := storeConfig("logs.db")
	if err != nil {
		return nil, err
	}

	cfg := map[string]any{
		"$schema":        "https://www.getbifrost.ai/schema",
		"encryption_key": encryptionKey,
		"client": map[string]any{
			"enable_logging":     true,
			"log_retention_days": retention,
			// Obot authenticates every request before it reaches Bifrost, so Bifrost's own
			// inference auth and virtual-key enforcement stay off. Obot attaches a virtual
			// key when one of its budget bindings applies to the caller.
			"enforce_auth_on_inference": false,
			"allowed_origins":           []string{"*"},
			"max_request_body_size_mb":  100,
		},
		"config_store": configStore,
		"logs_store":   logsStore,
	}
	if opts.CacheRedisAddr != "" {
		redis := map[string]any{"addr": opts.CacheRedisAddr}
		if opts.CacheRedisPassword != "" {
			redis["password"] = opts.CacheRedisPassword
		}
		cfg["vector_store"] = map[string]any{
			"enabled": true,
			"type":    "redis",
			"config":  redis,
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func postgresConfigFromDSN(dsn string) (map[string]any, error) {
	u, err := url.Parse(strings.Replace(dsn, "postgresql://", "postgres://", 1))
	if err != nil {
		return nil, fmt.Errorf("invalid bifrost postgres DSN: %w", err)
	}
	password, _ := u.User.Password()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "prefer"
	}
	return map[string]any{
		"host":           u.Hostname(),
		"port":           port,
		"user":           u.User.Username(),
		"password":       password,
		"db_name":        strings.TrimPrefix(u.Path, "/"),
		"ssl_mode":       sslMode,
		"max_idle_conns": 5,
		"max_open_conns": 20,
	}, nil
}
