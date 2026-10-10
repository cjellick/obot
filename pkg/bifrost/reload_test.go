package bifrost

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"testing"
	"time"

	providerutils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

// Two engines sharing one store stand in for two replicas.
func TestReloadPicksUpAnotherReplicasChanges(t *testing.T) {
	up := mockOpenAI(t)
	defer up.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	a, b := New(), New()
	for _, e := range []*Engine{a, b} {
		if err := e.Start(ctx, Options{DataDir: dir}); err != nil {
			t.Fatal(err)
		}
	}

	// a makes the change, so it has it; b loaded its configuration before the change.
	revision := int64(0)
	announce := func(context.Context) (int64, error) {
		revision++
		return revision, nil
	}
	if err := a.Change(ctx, func() (bool, bool) {
		if err := a.Do(ctx, http.MethodPost, "/api/providers", map[string]any{
			"provider":       "openai",
			"network_config": map[string]any{"base_url": up.URL, "allow_private_network": true, "default_request_timeout_in_seconds": 30},
		}, nil); err != nil {
			t.Fatal(err)
		}
		if err := a.Do(ctx, http.MethodPost, "/api/providers/openai/keys", map[string]any{"name": "k", "value": "sk-test", "models": []string{"*"}, "weight": 1}, nil); err != nil {
			t.Fatal(err)
		}
		return true, true
	}, announce); err != nil {
		t.Fatal(err)
	}
	chat := `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	if code, body := do(t, a, http.MethodPost, "/v1/chat/completions", chat); code != http.StatusOK {
		t.Fatalf("a should serve the new provider: %d %s", code, body)
	}
	if code, _ := do(t, b, http.MethodPost, "/v1/chat/completions", chat); code == http.StatusOK {
		t.Fatal("expected b to be stale before reloading")
	}

	reloaded, err := b.Reload(1)
	if err != nil || !reloaded {
		t.Fatalf("expected b to reload, got %v %v", reloaded, err)
	}
	if code, body := do(t, b, http.MethodPost, "/v1/chat/completions", chat); code != http.StatusOK {
		t.Fatalf("b should serve the new provider after reloading: %d %s", code, body)
	}
	if reloaded, _ := b.Reload(1); reloaded {
		t.Fatal("a revision already loaded must not reload again")
	}
	if reloaded, _ := a.Reload(1); reloaded {
		t.Fatal("the replica that made the change must not reload for it")
	}

	// A replica that missed an earlier change can't claim a later one it made: b has
	// revision 1, another replica made 2, and b's own change is 3.
	revision = 2
	if err := b.Change(ctx, func() (bool, bool) { return true, true }, announce); err != nil {
		t.Fatal(err)
	}
	if reloaded, _ := b.Reload(3); !reloaded {
		t.Fatal("b missed revision 2, so it must reload for revision 3")
	}

	// A change this engine isn't sure it holds (it failed partway) makes it reload too.
	revision = 3
	if err := a.Change(ctx, func() (bool, bool) { return true, false }, announce); err != nil {
		t.Fatal(err)
	}
	if reloaded, _ := a.Reload(4); !reloaded {
		t.Fatal("a must reload for a change it may not hold in memory")
	}

	// A failed announcement is reported.
	if err := a.Change(ctx, func() (bool, bool) { return true, true }, func(context.Context) (int64, error) {
		return 0, errors.New("storage unavailable")
	}); err == nil {
		t.Fatal("expected the failed announcement to be reported")
	}
}

func TestReloadReleasesTheReplacedGateway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := New()
	if err := e.Start(ctx, Options{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	// Warm up: the first reload starts process-wide workers that are shared, not leaked.
	if _, err := e.Reload(1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	before := runtime.NumGoroutine()
	for rev := int64(2); rev <= 5; rev++ {
		if _, err := e.Reload(rev); err != nil {
			t.Fatal(err)
		}
	}
	// Replaced gateways shut down in the background.
	deadline := time.Now().Add(20 * time.Second)
	for runtime.NumGoroutine() > before+10 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+10 {
		t.Fatalf("reloads leaked goroutines: %d before, %d after four reloads", before, after)
	}
}

func TestFailedReloadKeepsTheRunningGateway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := New()
	if err := e.Start(ctx, Options{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if providerutils.CapabilitiesFor(schemas.OpenAI, "gpt-4o") == nil {
		t.Skip("the model catalog has no capability data in this environment")
	}

	// Make the next boot fail: its stores point at a database that doesn't exist. A boot
	// that fails after its model catalog started clears the process-wide capability lookup
	// while releasing it; simulate that, since where exactly a boot fails varies.
	e.opts.PostgresDSN = "postgres://nobody:nothing@127.0.0.1:1/missing?sslmode=disable"
	providerutils.SetCapabilityResolver(nil)
	if reloaded, err := e.Reload(1); err == nil || reloaded {
		t.Fatalf("expected the reload to fail, got %v %v", reloaded, err)
	}
	if providerutils.CapabilitiesFor(schemas.OpenAI, "gpt-4o") == nil {
		t.Fatal("a failed reload must leave the running gateway's capability lookup in place")
	}
	if code, body := do(t, e, http.MethodGet, "/v1/models", ""); code >= 500 {
		t.Fatalf("the running gateway must keep serving: %d %s", code, body)
	}
}
