package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	gatewaydb "github.com/obot-platform/obot/pkg/gateway/db"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/storage/scheme"
	sservices "github.com/obot-platform/obot/pkg/storage/services"
	"github.com/obot-platform/obot/pkg/system"
	"gorm.io/datatypes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	testNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
)

func newSnapshot() *riskregister.Snapshot {
	return &riskregister.Snapshot{CollectedAt: testNow, WindowDays: riskregister.DefaultWindowDays}
}

func newGatewayClient(t *testing.T) *gclient.Client {
	t.Helper()

	storageServices, err := sservices.New(sservices.Config{DSN: "sqlite://:memory:"})
	if err != nil {
		t.Fatalf("create storage services: %v", err)
	}
	db, err := gatewaydb.New(storageServices.DB.DB, storageServices.DB.SQLDB, true)
	if err != nil {
		t.Fatalf("create gateway db: %v", err)
	}
	if err := db.AutoMigrate(); err != nil {
		t.Fatalf("migrate gateway db: %v", err)
	}
	c := gclient.New(t.Context(), db, nil, nil, nil, 10*time.Millisecond, 10, 90, 90, 90, true)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClaudeComplianceCollect(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/compliance/activities" || r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("unexpected request %s with headers %v", r.URL, r.Header)
		}
		query := r.URL.Query()
		if len(query["activity_types[]"]) != len(complianceProducts)+2 || query.Get("created_at.gte") != "2026-10-22T12:00:00Z" {
			t.Errorf("unexpected query %v", query)
		}
		page := compliancePage{HasMore: true, LastID: "activity_2", Data: []map[string]any{
			{"id": "activity_1", "type": "claude_chat_created", "created_at": "2026-11-20T10:00:00Z", "actor": map[string]any{"type": "user_actor", "email_address": "Ana@Example.com"}},
			{"id": "activity_2", "type": "claude_code_sign_in", "created_at": "2026-11-19T10:00:00Z", "actor": map[string]any{"type": "user_actor", "email_address": "ben@example.com"}},
		}}
		if query.Get("after_id") == "activity_2" {
			page = compliancePage{Data: []map[string]any{
				{"id": "activity_3", "type": "ccr_session_created", "created_at": "2026-11-18T10:00:00Z", "actor": map[string]any{"type": "user_actor", "email_address": "ana@example.com"}},
				{"id": "activity_4", "type": "mcp_server_created", "created_at": "2026-11-17T10:00:00Z", "mcp_server_url": "https://scraper.example.net/mcp", "actor": map[string]any{"type": "user_actor", "email_address": "ana@example.com"}},
				{"id": "activity_5", "type": "integration_user_connected", "created_at": "2026-11-17T09:00:00Z", "integration_name": "Google Drive", "actor": map[string]any{"type": "user_actor", "email_address": "ben@example.com"}},
				// An event with no signed-in user isn't counted as a person using a product.
				{"id": "activity_6", "type": "claude_chat_created", "created_at": "2026-11-17T08:00:00Z", "actor": map[string]any{"type": "system_actor"}},
			}}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	snapshot := newSnapshot()
	counts, err := (&ClaudeCompliance{APIKey: "key", BaseURL: server.URL, Client: server.Client()}).Collect(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || counts["events"] != 6 || counts["products"] != 2 || counts["connectors"] != 2 {
		t.Fatalf("requests = %d, counts = %v", requests, counts)
	}
	if len(snapshot.Activity) != 2 {
		t.Fatalf("activity = %+v", snapshot.Activity)
	}
	// Products sort by name, and uppercase sorts first.
	code, chat := snapshot.Activity[0], snapshot.Activity[1]
	if chat.Product != "Claude chat" || chat.Segment != types.RiskSegmentBusinessUser || len(chat.Users) != 1 || chat.Users[0] != "ana@example.com" || chat.Events != 1 {
		t.Errorf("chat activity = %+v", chat)
	}
	if code.Product != "Claude Code" || code.Segment != types.RiskSegmentCodingAgent || len(code.Users) != 2 || code.Events != 2 {
		t.Errorf("code activity = %+v", code)
	}
	if drive, scraper := snapshot.Connectors[0], snapshot.Connectors[1]; drive.Name != "Google Drive" || scraper.Name != "scraper.example.net" || scraper.URL != "https://scraper.example.net/mcp" || scraper.Users != 1 {
		t.Errorf("connectors = %+v", snapshot.Connectors)
	}
}

func TestClaudeComplianceReportsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"type":"permission_error"}}`, http.StatusForbidden)
	}))
	defer server.Close()

	if _, err := (&ClaudeCompliance{APIKey: "key", BaseURL: server.URL, Client: server.Client()}).Collect(t.Context(), newSnapshot()); err == nil {
		t.Fatal("expected an error for a 403 response")
	}
}

func TestOktaCollect(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "SSWS token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/api/v1/apps" && r.URL.Query().Get("after") == "":
			w.Header().Add("Link", fmt.Sprintf(`<%s/api/v1/apps?after=2>; rel="next"`, server.URL))
			_, _ = w.Write([]byte(`[{"id":"a1","label":"ChatGPT Enterprise","status":"ACTIVE"},{"id":"a2","label":"Salesforce","status":"ACTIVE"}]`))
		case r.URL.Path == "/api/v1/apps":
			_, _ = w.Write([]byte(`[{"id":"a3","label":"Devin","status":"ACTIVE"},{"id":"a4","label":"Cursor","status":"INACTIVE"}]`))
		case r.URL.Path == "/api/v1/apps/a1/users":
			_, _ = w.Write([]byte(`[{"id":"u1"},{"id":"u2"},{"id":"u3"}]`))
		case r.URL.Path == "/api/v1/apps/a3/users":
			_, _ = w.Write([]byte(`[{"id":"u1"}]`))
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	okta := &Okta{
		OrgURL:   server.URL,
		APIToken: "token",
		Client:   server.Client(),
		Classify: func(_ context.Context, labels []string) map[string]riskregister.AIApp {
			// Only the active apps the built-in list didn't recognize are sent to a model.
			if len(labels) != 2 || labels[0] != "Salesforce" || labels[1] != "Devin" {
				t.Errorf("classified labels = %v", labels)
			}
			return map[string]riskregister.AIApp{"Devin": {Name: "Devin", Vendor: "Cognition", Segment: types.RiskSegmentCodingAgent}}
		},
	}
	snapshot := newSnapshot()
	counts, err := okta.Collect(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if counts["apps"] != 4 || counts["aiApps"] != 3 || len(snapshot.Apps) != 3 {
		t.Fatalf("counts = %v, apps = %+v", counts, snapshot.Apps)
	}
	if app := snapshot.Apps[0]; app.Label != "ChatGPT Enterprise" || app.AssignedUsers != 3 || app.Match.ByModel {
		t.Errorf("first app = %+v", app)
	}
	if app := snapshot.Apps[1]; app.Label != "Devin" || app.AssignedUsers != 1 || !app.Match.ByModel || app.Match.Segment != types.RiskSegmentCodingAgent {
		t.Errorf("model-recognized app = %+v (%+v)", app, app.Match)
	}
	if app := snapshot.Apps[2]; app.Label != "Cursor" || app.Active || app.AssignedUsers != 0 {
		t.Errorf("inactive app = %+v", app)
	}
}

func TestOktaRefusesNextPageOnAnotherHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Link", `<https://attacker.example.com/api/v1/apps?after=2>; rel="next"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	if _, err := (&Okta{OrgURL: server.URL, APIToken: "token", Client: server.Client()}).Collect(t.Context(), newSnapshot()); err == nil {
		t.Fatal("expected an error when the next page is on another host")
	}
}

func TestAnthropicAdminCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/api_keys" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if r.Header.Get("x-api-key") != "sk-ant-admin-test" && r.Header.Get("Authorization") != "Bearer sk-ant-admin-test" {
			t.Errorf("admin key was not sent: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"apikey_1","type":"api_key","name":"ci","status":"active","created_at":"2026-03-01T00:00:00Z","scope":{"type":"workspace","workspace_id":"wrkspc_1"}},
			{"id":"apikey_2","type":"api_key","name":"old","status":"inactive","created_at":"2025-01-01T00:00:00Z","scope":{"type":"organization"}}
		],"has_more":false,"first_id":"apikey_1","last_id":"apikey_2"}`))
	}))
	defer server.Close()

	snapshot := newSnapshot()
	counts, err := (&AnthropicAdmin{AdminKey: "sk-ant-admin-test", BaseURL: server.URL, Client: server.Client()}).Collect(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if counts["apiKeys"] != 2 || counts["activeKeys"] != 1 || len(snapshot.APIKeys) != 2 {
		t.Fatalf("counts = %v, keys = %+v", counts, snapshot.APIKeys)
	}
	if key := snapshot.APIKeys[0]; key.ID != "apikey_1" || !key.Active || key.Scope != "workspace wrkspc_1" || key.CreatedAt.Year() != 2026 {
		t.Errorf("first key = %+v", key)
	}
	if key := snapshot.APIKeys[1]; key.Active || key.Scope != "all workspaces" {
		t.Errorf("second key = %+v", key)
	}
}

func TestRepositoryFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	servers, err := readMCPConfig(write(".mcp.json", `{"mcpServers":{
		"db":{"command":"npx","args":["-y","some-db-mcp"]},
		"docs":{"type":"sse","url":"https://docs.example.com/sse"}
	}}`), "mcpServers")
	if err != nil {
		t.Fatal(err)
	}
	observed := repoServers("acme/app", ".mcp.json", servers)
	if len(observed) != 2 || observed[0].Key != "acme/app:.mcp.json:db" || observed[0].Transport != "stdio" || observed[1].Transport != "sse" || observed[1].Location != "acme/app (.mcp.json)" {
		t.Fatalf("observed servers = %+v", observed)
	}

	if _, err := readMCPConfig(write("broken.json", `{"mcpServers":`), "mcpServers"); err == nil {
		t.Error("expected an error for malformed JSON")
	}

	settings := readClaudeSettings(write(".claude/settings.json", `{
		"enableAllProjectMcpServers": true,
		"permissions": {"defaultMode": "bypassPermissions", "allow": ["Read", "Bash(*)"]}
	}`), "acme/app", ".claude/settings.json")
	got := map[string]bool{}
	for _, setting := range settings {
		got[setting.Setting] = true
	}
	if len(settings) != 3 || !got[riskregister.RepoSettingBypassPermissions] || !got[riskregister.RepoSettingAllProjectMCP] || !got[riskregister.RepoSettingBroadAllow] {
		t.Fatalf("settings = %+v", settings)
	}

	if found := readClaudeSettings(write("ok/.claude/settings.json", `{"permissions":{"allow":["Bash(npm test:*)"]}}`), "acme/app", "ok/.claude/settings.json"); len(found) != 0 {
		t.Errorf("a narrow allow rule was reported: %+v", found)
	}
}

func TestGrantedTools(t *testing.T) {
	manifest := types.VMCPManifest{
		Components: []types.VMCPComponent{
			{ID: "github", ToolPrefix: "gh_", ToolOverrides: []types.ToolOverride{
				{Name: "create_issue", Enabled: true},
				{Name: "delete_repo", OverrideName: "remove_repository", Enabled: true},
				{Name: "hidden"},
			}},
			{ID: "slack", ToolOverrides: []types.ToolOverride{{Name: "post", Enabled: true}}},
			{ID: "unlisted"},
		},
	}
	tests := []struct {
		name      string
		profile   types.VMCPProfile
		want      []string
		countable bool
	}{
		{
			name: "named tools on one component",
			profile: types.VMCPProfile{Permissions: types.VMCPProfilePermissions{AllowedComponents: map[string]types.VMCPComponentSet{
				"github": {AllowedTools: []string{"delete_repo"}},
			}}},
			want:      []string{"gh_remove_repository"},
			countable: true,
		},
		{
			name: "every enabled tool on a component",
			profile: types.VMCPProfile{Permissions: types.VMCPProfilePermissions{AllowedComponents: map[string]types.VMCPComponentSet{
				"github": {},
				"slack":  {},
			}}},
			want:      []string{"gh_create_issue", "gh_remove_repository", "post"},
			countable: true,
		},
		{
			name:    "all components includes one with no tool list",
			profile: types.VMCPProfile{Permissions: types.VMCPProfilePermissions{AllowAllComponents: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, countable := grantedTools(manifest, tt.profile)
			if countable != tt.countable {
				t.Fatalf("countable = %v, want %v", countable, tt.countable)
			}
			var got []string
			for _, tool := range tools {
				got = append(got, tool.prefix+tool.visible)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("tools = %v, want %v", got, tt.want)
			}
		})
	}

	used := grantedTool{prefix: "gh_", visible: "create_issue"}
	if !used.usedIn([]string{"gh_create_issue"}) || used.usedIn([]string{"create_issue_comment"}) {
		t.Error("usedIn matched the wrong called names")
	}
}

// TestRunnerDayZero runs a whole collection against a new install: device scans and a catalog,
// no gateway traffic and no outside sources.
func TestRunnerDayZero(t *testing.T) {
	gateway := newGatewayClient(t)
	ctx := t.Context()

	storage := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&v1.MCPServerCatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "linear", Namespace: system.DefaultNamespace}, //nolint:modernize
		Spec: v1.MCPServerCatalogEntrySpec{Manifest: types.MCPServerCatalogEntryManifest{
			Runtime:      types.RuntimeRemote,
			RemoteConfig: &types.RemoteCatalogConfig{FixedURL: "https://mcp.linear.app/mcp"},
		}},
	}).Build()

	for device := range 6 {
		scan := gtypes.DeviceScan{
			DeviceID:  fmt.Sprintf("device-%d", device),
			Username:  fmt.Sprintf("user-%d", device),
			ScannedAt: testNow,
			Clients:   []gtypes.DeviceScanClient{{Name: "claude-code"}, {Name: "opencode"}},
			MCPServers: []gtypes.DeviceScanMCPServer{
				{Client: "claude-code", Name: "linear", Transport: "http", URL: "https://mcp.linear.app/mcp", ConfigHash: "hash-linear"},
				{Client: "claude-code", Name: "db", Transport: "stdio", Command: "npx", Args: datatypes.JSONSlice[string]{"-y", "some-db-mcp"}, ConfigHash: "hash-db"},
			},
		}
		if err := gateway.InsertDeviceScan(ctx, &scan); err != nil {
			t.Fatal(err)
		}
	}

	runner := NewRunner(gateway, storage, nil, Options{ServerURL: "https://obot.example.com", MCPRetentionDays: 90, LLMRetentionDays: 90})
	run, err := runner.Run(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if run.Found != 3 || run.New != 3 || run.Analyzed || run.FinishedAt == nil {
		t.Fatalf("run = %+v", run)
	}

	risks, err := gateway.ListRisks(ctx, gclient.RiskListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]gtypes.Risk{}
	for _, risk := range risks {
		got[risk.Fingerprint] = risk
	}
	server, ok := got[riskregister.TypeUnapprovedMCPServer+":sentry:hash-db"]
	if !ok || server.Severity != string(types.RiskSeverityHigh) || server.Metrics["devices"] != 6 || server.Treatment == nil {
		t.Errorf("unapproved server entry = %+v", server)
	}
	if _, ok := got[riskregister.TypeUnapprovedMCPServer+":sentry:hash-linear"]; ok {
		t.Error("a server in the catalog was reported as unapproved")
	}
	if _, ok := got[riskregister.TypeClientNotEnforced+":opencode"]; !ok {
		t.Error("missing entry for a client outside enforcement")
	}
	if _, ok := got[riskregister.TypeRecordsExpire+":audit-logs"]; !ok {
		t.Error("missing entry for short audit retention")
	}

	if run.Profile == nil || len(run.Profile.Segments) == 0 {
		t.Fatalf("profile = %+v", run.Profile)
	}
	coding := run.Profile.Segments[0]
	if coding.Segment != types.RiskSegmentCodingAgent || coding.Devices != 6 || coding.Users != 6 || coding.OpenRisks != 2 || len(coding.Tools) != 2 {
		t.Errorf("coding segment = %+v", coding)
	}

	// Outside sources that aren't configured are listed as skipped, not as failures.
	skipped := 0
	for _, source := range run.Sources {
		if source.Skipped != "" {
			skipped++
		}
		if !source.OK {
			t.Errorf("source %s failed: %s", source.Kind, source.Error)
		}
	}
	if skipped != 4 {
		t.Errorf("skipped sources = %d, want 4: %+v", skipped, run.Sources)
	}

	// A second run finds the same things and creates nothing new.
	if run, err = runner.Run(ctx, "test"); err != nil || run.New != 0 || run.Found != 3 {
		t.Fatalf("second run = %+v, %v", run, err)
	}
}
