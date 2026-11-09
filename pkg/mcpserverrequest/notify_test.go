package mcpserverrequest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
)

type memCredentials struct {
	credentials map[string]gtypes.Credential
}

func TestNotifyRequestedPostsToSlack(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	n := NewNotifier(srv.URL, "https://obot.example.com/", nil)
	err := n.NotifyRequested(context.Background(), types.MCPServerRequest{
		ID:            "msrq1abc",
		RequesterName: "Ada <script>",
		Name:          "Linear",
		Reason:        "Track issues\nfrom Claude",
		Runtime:       types.RuntimeRemote,
		RemoteURL:     "https://mcp.linear.app/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	body := string(b)
	for _, want := range []string{
		"https://obot.example.com/mcp-server-requests/msrq1abc",
		"Ada \\u0026lt;script\\u0026gt;",
		"\\u003eTrack issues\\n\\u003efrom Claude",
		"https://mcp.linear.app/mcp",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("message missing %q:\n%s", want, body)
		}
	}
}

func TestNotifierDisabled(t *testing.T) {
	n := NewNotifier("", "https://obot.example.com", nil)
	if n.Enabled(context.Background()) {
		t.Fatal("a notifier without a webhook must be disabled")
	}
	if err := n.NotifyRequested(context.Background(), types.MCPServerRequest{}); err != nil {
		t.Fatal(err)
	}
	var nilNotifier *Notifier
	if nilNotifier.Enabled(context.Background()) {
		t.Fatal("nil notifier must be disabled")
	}
}

func TestNotifyRequestedReportsSlackErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid_token", http.StatusForbidden)
	}))
	defer srv.Close()
	err := NewNotifier(srv.URL, "https://obot.example.com", nil).NotifyRequested(context.Background(), types.MCPServerRequest{})
	if err == nil || !strings.Contains(err.Error(), "invalid_token") {
		t.Fatalf("err = %v", err)
	}
}

func (m *memCredentials) RevealCredential(_ context.Context, contexts []string, name string) (gtypes.Credential, error) {
	for _, c := range contexts {
		if cred, ok := m.credentials[c+"/"+name]; ok {
			return cred, nil
		}
	}
	return gtypes.Credential{}, gclient.CredentialNotFoundError{Contexts: contexts, Name: name}
}

func (m *memCredentials) UpsertCredential(_ context.Context, credential gtypes.Credential) error {
	m.credentials[credential.Context+"/"+credential.Name] = credential
	return nil
}

func (m *memCredentials) DeleteCredential(_ context.Context, context, name string) (bool, error) {
	_, ok := m.credentials[context+"/"+name]
	delete(m.credentials, context+"/"+name)
	return ok, nil
}

func TestNotifierWebhookSetInObot(t *testing.T) {
	ctx := context.Background()
	posted := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posted++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := &memCredentials{credentials: map[string]gtypes.Credential{}}
	n := NewNotifier("", "https://obot.example.com", store)
	if settings, err := n.Settings(ctx); err != nil || settings.SlackWebhookConfigured || settings.SetViaDeployment {
		t.Fatalf("no webhook yet: %+v (%v)", settings, err)
	}
	if err := n.SetWebhook(ctx, "not a url"); err == nil {
		t.Fatal("an invalid URL should be rejected")
	}
	if err := n.SetWebhook(ctx, srv.URL+"/services/T1/B2/abcd1234"); err != nil {
		t.Fatal(err)
	}
	settings, err := n.Settings(ctx)
	if err != nil || !settings.SlackWebhookConfigured || settings.SetViaDeployment || settings.SlackWebhookHint == "" || strings.Contains(settings.SlackWebhookHint, "T1/B2") {
		t.Fatalf("webhook set in Obot: %+v (%v)", settings, err)
	}
	if err := n.SendTest(ctx); err != nil || posted != 1 {
		t.Fatalf("test message: posted %d (%v)", posted, err)
	}
	if err := n.SetWebhook(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if n.Enabled(ctx) {
		t.Fatal("removing the webhook should disable alerts")
	}
}

func TestNotifierWebhookSetAtDeploymentIsReadOnly(t *testing.T) {
	ctx := context.Background()
	n := NewNotifier("https://hooks.slack.com/services/T1/B2/secret", "https://obot.example.com", &memCredentials{credentials: map[string]gtypes.Credential{}})
	settings, err := n.Settings(ctx)
	if err != nil || !settings.SetViaDeployment || !settings.SlackWebhookConfigured {
		t.Fatalf("deployment webhook: %+v (%v)", settings, err)
	}
	if err := n.SetWebhook(ctx, "https://hooks.slack.com/services/other"); !errors.Is(err, ErrSetViaDeployment) {
		t.Fatalf("changing a deployment webhook should fail, got %v", err)
	}
}
