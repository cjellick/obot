package bifrost

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Set OBOT_TEST_POSTGRES_DSN to a Postgres database the test may create databases next to.
func TestDerivePostgresDSNAndPostgresStores(t *testing.T) {
	dsn := os.Getenv("OBOT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OBOT_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	derived, err := DerivePostgresDSN(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(derived, "_llm_gateway") {
		t.Fatalf("unexpected derived DSN %q", derived)
	}
	// Deriving again finds the existing database.
	if again, err := DerivePostgresDSN(ctx, dsn); err != nil || again != derived {
		t.Fatalf("second derive: %q, %v", again, err)
	}

	e := New()
	if err := e.Start(ctx, Options{DataDir: t.TempDir(), PostgresDSN: derived}); err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown()
	if code, body := do(t, e, "GET", "/api/providers", ""); code != 200 {
		t.Fatalf("GET /api/providers: %d %s", code, body)
	}
}
