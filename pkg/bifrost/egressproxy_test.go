package bifrost

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestApplyEgressProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	e := New()
	if err := e.Start(ctx, Options{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	// A built-in provider, and a custom one like the legacy import creates.
	for _, p := range []map[string]any{
		{"provider": "openai"},
		{
			"provider":               "generic-responses",
			"custom_provider_config": map[string]any{"base_provider_type": "openai"},
			"network_config":         map[string]any{"base_url": "http://10.255.255.1/v1", "allow_private_network": true, "default_request_timeout_in_seconds": 300},
		},
	} {
		if err := e.Do(ctx, http.MethodPost, "/api/providers", p, nil); err != nil {
			t.Fatal(err)
		}
	}

	// Set after starting: the engine's own startup downloads would otherwise use it.
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("NO_PROXY", "")
	e.ApplyEgressProxy(ctx)

	var resp struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := e.Do(ctx, http.MethodGet, "/api/providers", nil, &resp); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, p := range resp.Providers {
		name, _ := p["name"].(string)
		if name != "openai" && name != "generic-responses" {
			continue
		}
		seen++
		if p["proxy_config"] == nil {
			t.Errorf("provider %s did not get the egress proxy", name)
		}
		if name == "generic-responses" && p["custom_provider_config"] == nil {
			t.Errorf("the custom provider lost its configuration")
		}
	}
	if seen != 2 {
		t.Fatalf("expected both providers, got %v", resp.Providers)
	}
}
