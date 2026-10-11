//go:build e2e

// Package llmgatewayupgrade checks an upgrade from the main branch to this branch's LLM
// gateway: an installation with message policies and daily token limits is run on main,
// then two replicas of the new binary start on the same database at once.
//
// Run it with `make test-e2e-llm-gateway-upgrade`. It builds main in a temporary git
// worktree unless OBOT_E2E_MAIN_BINARY points at a main build.
package llmgatewayupgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/system"
	"github.com/obot-platform/obot/tests/e2e/harness"
	"github.com/tidwall/gjson"
)

const (
	dailyInput   = 5000
	dailyOutput  = 2000
	legacyAPIKey = "e2e-legacy-openai-key"

	// fakeProviderDaemon is a provider daemon that serves one model, gpt-legacy.
	fakeProviderDaemon = `package main

import (
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(` + "`" + `{"object":"list","data":[{"id":"gpt-legacy","object":"model","metadata":{"usage":"llm"}}]}` + "`" + `))
	})
	_ = http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), nil)
}
`
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// buildMain builds the main branch's obot binary in a temporary worktree.
func buildMain(t *testing.T, dir string) string {
	t.Helper()
	if p := os.Getenv("OBOT_E2E_MAIN_BINARY"); p != "" {
		return p
	}
	root := repoRoot()
	ref := os.Getenv("OBOT_E2E_MAIN_REF")
	if ref == "" {
		ref = "main"
	}
	src := filepath.Join(dir, "main-src")
	run := func(cwd string, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}
	run(root, "git", "worktree", "add", "--detach", src, ref)
	t.Cleanup(func() {
		cmd := exec.Command("git", "worktree", "remove", "--force", src)
		cmd.Dir = root
		_ = cmd.Run()
	})
	out := filepath.Join(dir, "obot-main")
	start := time.Now()
	run(src, "go", "build", "-tags", "kustomize_disable_go_plugin_support", "-o", out, ".")
	t.Logf("built %s in %s", ref, time.Since(start).Round(time.Second))
	return out
}

func TestUpgradeFromMain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	dir := os.Getenv("OBOT_E2E_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	t.Logf("replica logs and data in %s", dir)
	branch := os.Getenv("OBOT_E2E_BINARY")
	if branch == "" {
		branch = filepath.Join(repoRoot(), "bin", "obot")
	}
	branchBinary, err := harness.PrepareBinary(branch, filepath.Join(dir, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	mainBinary, err := harness.PrepareBinary(buildMain(t, dir), filepath.Join(dir, "bin-main"))
	if err != nil {
		t.Fatal(err)
	}

	mock, err := harness.StartMockLLM()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	mock.SetModels("e2e-upgrade-anthropic", "claude-upgrade")

	cluster, err := harness.NewCluster(ctx, harness.ClusterOptions{
		Name:             "obot-e2e-upgrade-" + harness.RandomSuffix(),
		Dir:              dir,
		PretendContainer: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cluster.Close(); err != nil {
			t.Logf("cleanup: %v", err)
		}
		// Logs and data stay for inspection; the binary copies are large.
		for _, sub := range []string{"bin", "bin-main", "obot-main"} {
			_ = os.RemoveAll(filepath.Join(dir, sub))
		}
	}()

	// Obot images ship a provider registry; this one has only the OpenAI model provider, whose
	// daemon does nothing, so a legacy credential can be configured.
	registry := writeProviderRegistry(t, filepath.Join(dir, "provider-registry"))
	limits := []string{
		"--daily-user-input-token-limit", fmt.Sprint(dailyInput),
		"--daily-user-output-token-limit", fmt.Sprint(dailyOutput),
		"--provider-registries", registry,
	}
	policiesOn := map[string]string{"OBOT_SERVER_ENABLE_MESSAGE_POLICIES": "true"}

	// --- The old installation, on main. ---
	old, err := cluster.StartReplica(ctx, "main", mainBinary, limits, policiesOn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := cluster.ActivateAdmin(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	userEmail, userPassword := "legacy-user@e2e.example.com", "E2e-Legacy-Passw0rd!"
	admin.Post(t, "/api/local-auth/users", map[string]any{"email": userEmail, "password": userPassword, "requirePasswordChange": false}, nil)
	user, err := harness.Login(ctx, old.BaseURL, userEmail, userPassword)
	if err != nil {
		t.Fatal(err)
	}
	var me types.User
	user.Get(t, "/api/me", &me)
	// Obot has no API for personal token limits; they are set on the user record.
	if _, err := cluster.PG.Exec(ctx, cluster.DB, fmt.Sprintf("update users set daily_input_tokens_limit = 15 where id = %s", me.ID)); err != nil {
		t.Fatal(err)
	}

	type policy struct {
		manifest types.MessagePolicyManifest
		id       string
		applyTo  string
		subjects []map[string]string
	}
	policies := []*policy{
		{manifest: types.MessagePolicyManifest{DisplayName: "No travel booking", Definition: "Do not help book travel above economy class.",
			Direction: types.PolicyDirectionUserMessage, Subjects: []types.Subject{{Type: types.SubjectTypeSelector, ID: "*"}}},
			applyTo: "input", subjects: []map[string]string{{"type": "everyone"}}},
		{manifest: types.MessagePolicyManifest{DisplayName: "No destructive tools", Definition: "Never call tools that delete data.",
			Direction: types.PolicyDirectionToolCalls, Subjects: []types.Subject{{Type: types.SubjectTypeUser, ID: me.ID}}},
			applyTo: "output", subjects: []map[string]string{{"type": "user", "id": me.ID}}},
		{manifest: types.MessagePolicyManifest{DisplayName: "No secrets either way", Definition: "Do not reveal credentials.",
			Direction: types.PolicyDirectionBoth, Subjects: []types.Subject{{Type: types.SubjectTypeGroup, ID: "e2e-group"}}},
			applyTo: "both", subjects: []map[string]string{{"type": "group", "id": "e2e-group"}}},
	}
	for _, p := range policies {
		var created types.MessagePolicy
		admin.Post(t, "/api/message-policies", p.manifest, &created)
		p.id = created.ID
	}

	// A legacy model provider credential, which the gateway imports on its first start.
	legacyConfigured := false
	harness.Eventually(t, time.Minute, "the legacy OpenAI provider from the registry", func() error {
		if resp := admin.Do(t, http.MethodGet, "/api/model-providers/"+system.OpenAIModelProvider, nil); resp.Status != http.StatusOK {
			return fmt.Errorf("%s", resp)
		}
		return nil
	})
	if resp := admin.Do(t, http.MethodPost, "/api/model-providers/"+system.OpenAIModelProvider+"/configure", map[string]string{
		"OBOT_OPENAI_MODEL_PROVIDER_API_KEY": legacyAPIKey,
	}); resp.Status < 300 {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			var mp struct {
				Configured bool `json:"configured"`
			}
			admin.Get(t, "/api/model-providers/"+system.OpenAIModelProvider, &mp)
			if mp.Configured {
				legacyConfigured = true
				break
			}
			time.Sleep(time.Second)
		}
	} else {
		t.Logf("could not configure the legacy OpenAI provider on main: %s", resp)
	}
	if !legacyConfigured {
		t.Logf("the legacy OpenAI provider never reported configured on main; the import check is skipped")
	}

	// The default llm alias and an access policy refer to the legacy provider's model, which
	// the gateway must move them off.
	var legacyModelID string
	if legacyConfigured {
		harness.Eventually(t, time.Minute, "the legacy model on main", func() error {
			var list types.ModelList
			admin.Get(t, "/api/models?all=true", &list)
			for _, m := range list.Items {
				if m.ModelProvider == system.OpenAIModelProvider && m.TargetModel == "gpt-legacy" {
					legacyModelID = m.ID
					return nil
				}
			}
			return fmt.Errorf("not synced")
		})
		admin.Put(t, "/api/default-model-aliases/llm", types.DefaultModelAliasManifest{Alias: "llm", Model: legacyModelID}, nil)
		admin.Post(t, "/api/model-access-policies", types.ModelAccessPolicyManifest{
			DisplayName: "legacy model",
			Subjects:    []types.Subject{{Type: types.SubjectTypeSelector, ID: "*"}},
			Models:      []types.ModelResource{{ID: legacyModelID}},
		}, nil)
	}

	if err := old.Stop(); err != nil {
		t.Fatal(err)
	}
	if lines := harness.PanicLines(old.Log()); len(lines) > 0 {
		t.Fatalf("main crashed:\n%s", strings.Join(lines, "\n"))
	}

	// --- The upgrade: two replicas of the new binary at once. ---
	var (
		wg       sync.WaitGroup
		replicas = make([]*harness.Replica, 2)
		errs     = make([]error, 2)
	)
	for i := range replicas {
		wg.Go(func() {
			replicas[i], errs[i] = cluster.StartReplica(ctx, fmt.Sprintf("upgraded-%d", i), branchBinary, limits, policiesOn)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	admin, err = harness.Login(ctx, replicas[0].BaseURL, harness.AdminEmail, harness.AdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range replicas {
		a := admin.On(r.BaseURL)
		harness.Eventually(t, 2*time.Minute, "gateway ready on "+r.Config.Name, func() error {
			resp := a.Do(t, http.MethodGet, "/api/llm-gateway/status", nil)
			if !gjson.GetBytes(resp.Body, "ready").Bool() {
				return fmt.Errorf("status %s", resp)
			}
			return nil
		})
	}

	t.Run("default daily token limit budget", func(t *testing.T) {
		var list struct {
			Items []struct {
				ID              string `json:"id"`
				Name            string `json:"name"`
				Enabled         bool   `json:"enabled"`
				PerUser         bool   `json:"perUser"`
				ExemptAdmins    bool   `json:"exemptAdmins"`
				UserTokenLimits bool   `json:"userTokenLimits"`
				Limits          []struct {
					Metric string  `json:"metric"`
					Amount float64 `json:"amount"`
					Window string  `json:"window"`
				} `json:"limits"`
				Subjects []struct {
					Type string `json:"type"`
				} `json:"subjects"`
			} `json:"items"`
		}
		admin.Get(t, "/api/llm-gateway/budgets", &list)
		if len(list.Items) != 1 {
			t.Fatalf("want exactly one budget after the upgrade, got %d: %+v", len(list.Items), list.Items)
		}
		b := list.Items[0]
		if b.Name != "Daily token limit" || !b.Enabled || !b.PerUser || !b.ExemptAdmins || !b.UserTokenLimits ||
			len(b.Subjects) != 1 || b.Subjects[0].Type != "everyone" {
			t.Fatalf("unexpected default budget: %+v", b)
		}
		got := map[string]float64{}
		for _, l := range b.Limits {
			if l.Window != "1d" {
				t.Errorf("limit %s has window %s, want 1d", l.Metric, l.Window)
			}
			got[l.Metric] = l.Amount
		}
		if got["input_tokens"] != dailyInput || got["output_tokens"] != dailyOutput || len(got) != 2 {
			t.Fatalf("default budget limits %v, want input %d and output %d", got, dailyInput, dailyOutput)
		}
	})

	t.Run("message policies became LLM judge guardrails", func(t *testing.T) {
		var profiles struct {
			Items []struct {
				ID     string          `json:"id"`
				Name   string          `json:"name"`
				Type   string          `json:"type"`
				Config json.RawMessage `json:"config"`
			} `json:"items"`
		}
		var rules struct {
			Items []struct {
				ID         string              `json:"id"`
				Name       string              `json:"name"`
				Enabled    bool                `json:"enabled"`
				FailClosed bool                `json:"failClosed"`
				Target     string              `json:"target"`
				ApplyTo    string              `json:"applyTo"`
				Action     string              `json:"action"`
				ProfileIDs []string            `json:"profileIDs"`
				Subjects   []map[string]string `json:"subjects"`
			} `json:"items"`
		}
		admin.Get(t, "/api/llm-gateway/guardrails/profiles", &profiles)
		admin.Get(t, "/api/llm-gateway/guardrails/rules", &rules)
		if len(rules.Items) != len(policies) || len(profiles.Items) != len(policies) {
			t.Fatalf("want %d rules and profiles, got %d rules and %d profiles", len(policies), len(rules.Items), len(profiles.Items))
		}
		for _, p := range policies {
			pi := slices.IndexFunc(profiles.Items, func(x struct {
				ID     string          `json:"id"`
				Name   string          `json:"name"`
				Type   string          `json:"type"`
				Config json.RawMessage `json:"config"`
			}) bool {
				return x.Name == p.manifest.DisplayName
			})
			if pi < 0 {
				t.Fatalf("no detector for policy %q", p.manifest.DisplayName)
			}
			profile := profiles.Items[pi]
			if profile.Type != "llm-judge" || gjson.GetBytes(profile.Config, "definition").String() != p.manifest.Definition {
				t.Fatalf("detector for %q: %+v %s", p.manifest.DisplayName, profile, profile.Config)
			}
			found := false
			for _, r := range rules.Items {
				if r.Name != p.manifest.DisplayName {
					continue
				}
				found = true
				// Message policies blocked when the judge failed, so the rules fail closed.
				if !r.Enabled || !r.FailClosed || r.Target != "llm" || r.Action != "block" || r.ApplyTo != p.applyTo ||
					len(r.ProfileIDs) != 1 || r.ProfileIDs[0] != profile.ID {
					t.Fatalf("rule for %q: %+v", p.manifest.DisplayName, r)
				}
				if mustJSON(r.Subjects) != mustJSON(p.subjects) {
					t.Fatalf("rule for %q has subjects %v, want %v", p.manifest.DisplayName, r.Subjects, p.subjects)
				}
			}
			if !found {
				t.Fatalf("no rule for policy %q", p.manifest.DisplayName)
			}
		}
		// The message policy API is gone; LLM judge guardrails replace it.
		if resp := admin.Do(t, http.MethodGet, "/api/message-policies", nil); resp.Status != http.StatusNotFound && resp.Status != http.StatusForbidden {
			t.Errorf("listing message policies: want the API gone, got %s", resp)
		}
	})

	t.Run("legacy provider credentials were imported", func(t *testing.T) {
		if !legacyConfigured {
			t.Skip("the legacy provider could not be configured on main")
		}
		for _, r := range replicas {
			a := admin.On(r.BaseURL)
			harness.Eventually(t, time.Minute, "imported provider on "+r.Config.Name, func() error {
				var keys struct {
					Keys []struct {
						Name  string          `json:"name"`
						Value json.RawMessage `json:"value"`
					} `json:"keys"`
				}
				resp := a.Do(t, http.MethodGet, "/api/llm-gateway/admin/providers/openai/keys", nil)
				if resp.Status != http.StatusOK {
					return fmt.Errorf("keys: %s", resp)
				}
				resp.JSON(t, &keys)
				var names []string
				for _, k := range keys.Keys {
					names = append(names, k.Name)
				}
				if len(names) != 1 || names[0] != "imported-"+system.OpenAIModelProvider {
					return fmt.Errorf("want one imported key, got %v", names)
				}
				return nil
			})
		}
	})

	t.Run("references to the legacy model moved to the gateway", func(t *testing.T) {
		if !legacyConfigured {
			t.Skip("the legacy provider could not be configured on main")
		}
		// The imported provider talks to OpenAI; point it at the mock, which serves the same
		// model for the imported key.
		mock.SetModels(legacyAPIKey, "gpt-legacy")
		var provider map[string]any
		admin.Get(t, "/api/llm-gateway/admin/providers/openai", &provider)
		nc, _ := provider["network_config"].(map[string]any)
		nc["base_url"], nc["allow_private_network"] = mock.URL, true
		update := map[string]any{"network_config": nc}
		for _, k := range []string{"keys", "concurrency_and_buffer_size", "proxy_config", "custom_provider_config"} {
			if v, ok := provider[k]; ok {
				update[k] = v
			}
		}
		admin.Put(t, "/api/llm-gateway/admin/providers/openai", update, nil)
		admin.Post(t, "/api/llm-gateway/sync-models", map[string]any{}, nil)

		var gatewayModelID string
		harness.Eventually(t, 2*time.Minute, "the llm alias on the gateway model", func() error {
			var list types.ModelList
			admin.Get(t, "/api/models?all=true", &list)
			for _, m := range list.Items {
				if m.TargetModel == "openai/gpt-legacy" {
					gatewayModelID = m.ID
				}
			}
			var alias types.DefaultModelAlias
			admin.Get(t, "/api/default-model-aliases/llm", &alias)
			if gatewayModelID == "" || alias.Model != gatewayModelID {
				return fmt.Errorf("gateway model %q, llm alias %q", gatewayModelID, alias.Model)
			}
			return nil
		})
		var policies struct {
			Items []types.ModelAccessPolicy `json:"items"`
		}
		admin.Get(t, "/api/model-access-policies", &policies)
		found := false
		for _, p := range policies.Items {
			if p.DisplayName != "legacy model" {
				continue
			}
			found = true
			if len(p.Models) != 1 || p.Models[0].ID != gatewayModelID {
				t.Errorf("access policy models %v, want the gateway model %s", p.Models, gatewayModelID)
			}
		}
		if !found {
			t.Error("the access policy referring to the legacy model is gone")
		}
		harness.Eventually(t, time.Minute, "the legacy model and provider to be deleted", func() error {
			if resp := admin.Do(t, http.MethodGet, "/api/models/"+legacyModelID, nil); resp.Status != http.StatusNotFound {
				return fmt.Errorf("legacy model: %s", resp)
			}
			if resp := admin.Do(t, http.MethodGet, "/api/model-providers/"+system.OpenAIModelProvider, nil); resp.Status != http.StatusNotFound {
				return fmt.Errorf("legacy provider: %s", resp)
			}
			return nil
		})
	})

	t.Run("personal token limit applies through the migrated budget", func(t *testing.T) {
		a := admin
		a.Post(t, "/api/llm-gateway/admin/providers", map[string]any{
			"provider":       "anthropic",
			"network_config": map[string]any{"base_url": mock.URL, "allow_private_network": true},
		}, nil)
		a.Post(t, "/api/llm-gateway/admin/providers/anthropic/keys", map[string]any{"name": "k", "value": "e2e-upgrade-anthropic", "models": []string{"*"}, "weight": 1}, nil)
		a.Post(t, "/api/llm-gateway/sync-models", map[string]any{}, nil)
		var modelID string
		harness.Eventually(t, 2*time.Minute, "the mock model in Obot", func() error {
			var list types.ModelList
			a.Get(t, "/api/models?all=true", &list)
			for _, m := range list.Items {
				if m.TargetModel == "anthropic/claude-upgrade" {
					modelID = m.ID
					return nil
				}
			}
			return fmt.Errorf("not synced")
		})
		a.Post(t, "/api/model-access-policies", types.ModelAccessPolicyManifest{
			DisplayName: "upgrade",
			Subjects:    []types.Subject{{Type: types.SubjectTypeSelector, ID: "*"}},
			Models:      []types.ModelResource{{ID: modelID}},
		}, nil)
		var key struct {
			Key string `json:"key"`
		}
		session, err := harness.Login(ctx, replicas[1].BaseURL, userEmail, userPassword)
		if err != nil {
			t.Fatal(err)
		}
		session.Post(t, "/api/api-keys", map[string]any{"name": "upgrade", "canAccessLLMProxy": true}, &key)
		call := func(r *harness.Replica) *harness.Response {
			return harness.NewAPIKeyClient(r.BaseURL, key.Key).Do(t, http.MethodPost, "/api/llm-proxy/v1/chat/completions", map[string]any{
				"model": "anthropic/claude-upgrade", "messages": []any{map[string]any{"role": "user", "content": "hi"}},
			})
		}
		// The policy converted for everyone fails closed, as message policies did, and no
		// judge model is configured here, so it blocks.
		harness.Eventually(t, time.Minute, "the converted message policy to block", func() error {
			resp := call(replicas[0])
			if resp.Status != http.StatusBadRequest || !strings.Contains(string(resp.Body), "guardrail_intervention") ||
				!strings.Contains(string(resp.Body), "No travel booking") {
				return fmt.Errorf("%s", resp)
			}
			return nil
		})
		// Disable the converted rules to reach the budget.
		var rules struct {
			Items []map[string]any `json:"items"`
		}
		a.Get(t, "/api/llm-gateway/guardrails/rules", &rules)
		for _, r := range rules.Items {
			r["enabled"] = false
			a.Put(t, "/api/llm-gateway/guardrails/rules/"+fmt.Sprint(r["id"]), r, nil)
		}
		for _, r := range replicas {
			harness.Eventually(t, time.Minute, "a gateway request on "+r.Config.Name, func() error {
				if resp := call(r); resp.Status != http.StatusOK {
					return fmt.Errorf("%s", resp)
				}
				return nil
			})
		}
		// Two requests of 10 input tokens use up the personal limit of 15.
		resp := call(replicas[0])
		if resp.Status != http.StatusTooManyRequests || !strings.Contains(string(resp.Body), "Daily token limit") || !strings.Contains(string(resp.Body), "of 15") {
			t.Fatalf("want the migrated budget to apply the personal limit of 15, got %s", resp)
		}
	})

	t.Run("no replica crashed", func(t *testing.T) {
		for _, r := range replicas {
			if r.Exited() {
				t.Errorf("%s exited; log %s", r.Config.Name, r.LogPath)
			}
			if lines := harness.PanicLines(r.Log()); len(lines) > 0 {
				t.Errorf("%s crashed:\n%s", r.Config.Name, strings.Join(lines, "\n"))
			}
		}
		out, err := cluster.PG.Exec(ctx, cluster.DB, "select count(*) from migrations where name in ('llm_gateway_default_budget', 'guardrails_message_policies')")
		if err != nil {
			t.Logf("could not read migration markers: %v", err)
		} else if out != "2" {
			t.Errorf("want both migration markers recorded once, got %s", out)
		}
	})
}

// writeProviderRegistry writes a provider registry with an OpenAI model provider and an
// auth provider, whose daemon serves an empty model list.
func writeProviderRegistry(t *testing.T, dir string) string {
	t.Helper()
	for _, sub := range []string{"model-providers", "auth-providers", "daemon-src"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(dir, "daemon-src", "main.go")
	if err := os.WriteFile(src, []byte(fakeProviderDaemon), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "fake-provider"), src)
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GO111MODULE=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the fake provider daemon: %v\n%s", err, out)
	}
	files := map[string]string{
		"model-providers/openai-model-provider.yaml": `name: OpenAI
command: fake-provider
description: e2e stand-in for the OpenAI model provider
requiredConfigurationParameters:
  - name: OBOT_OPENAI_MODEL_PROVIDER_API_KEY
    friendlyName: API Key
    sensitive: true
`,
		"auth-providers/e2e-auth-provider.yaml": `name: E2E Auth
command: fake-provider
description: e2e stand-in auth provider, never configured
groupIDPrefix: "e2e/"
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
