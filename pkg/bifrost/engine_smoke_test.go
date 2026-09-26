package bifrost

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEngineSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e := New()
	if err := e.Start(ctx, Options{DataDir: t.TempDir(), LogLevel: "info"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/health", "/api/providers", "/api/config", "/api/governance/virtual-keys", "/api/plugins", "/v1/models"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		b, _ := io.ReadAll(rec.Body)
		if len(b) > 400 {
			b = b[:400]
		}
		t.Logf("%s -> %d %s", p, rec.Code, b)
	}
}
