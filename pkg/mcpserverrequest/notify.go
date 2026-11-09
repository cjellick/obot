// Package mcpserverrequest alerts administrators about users' requests to add MCP servers.
package mcpserverrequest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
)

const (
	credentialContext = "mcp-server-requests"
	credentialName    = "slack-webhook"
	credentialKey     = "url"
)

// ErrSetViaDeployment is returned when an administrator tries to change a webhook that was
// set at deployment.
var (
	ErrSetViaDeployment = errors.New("the Slack webhook was set when Obot was deployed and can't be changed here")
)

// CredentialStore keeps the webhook an administrator set in Obot, encrypted.
type CredentialStore interface {
	RevealCredential(ctx context.Context, contexts []string, name string) (gtypes.Credential, error)
	UpsertCredential(ctx context.Context, credential gtypes.Credential) error
	DeleteCredential(ctx context.Context, context, name string) (bool, error)
}

// Notifier posts new requests to a Slack incoming webhook. The webhook comes from the
// deployment when one was configured there, and otherwise from what an administrator set
// in Obot. A Notifier without a webhook does nothing.
type Notifier struct {
	deployedURL string
	store       CredentialStore
	baseURL     string
	client      *http.Client
}

// Settings is where the Slack webhook stands, without revealing it.
type Settings struct {
	// SlackWebhookConfigured reports whether new requests are posted to Slack.
	SlackWebhookConfigured bool `json:"slackWebhookConfigured"`
	// SlackWebhookHint is enough of the webhook to recognize it.
	SlackWebhookHint string `json:"slackWebhookHint,omitempty"`
	// SetViaDeployment reports that the webhook was set when Obot was deployed, so it can't
	// be changed in Obot.
	SetViaDeployment bool `json:"setViaDeployment"`
}

// NewNotifier creates a notifier. deployedURL is the webhook set at deployment, which
// administrators cannot change in Obot; store may be nil.
func NewNotifier(deployedURL, baseURL string, store CredentialStore) *Notifier {
	return &Notifier{
		deployedURL: strings.TrimSpace(deployedURL),
		store:       store,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

// webhookURL is the webhook to post to, or "" when there is none.
func (n *Notifier) webhookURL(ctx context.Context) (string, error) {
	if n == nil {
		return "", nil
	}
	if n.deployedURL != "" {
		return n.deployedURL, nil
	}
	if n.store == nil {
		return "", nil
	}
	credential, err := n.store.RevealCredential(ctx, []string{credentialContext}, credentialName)
	if err != nil {
		if _, ok := errors.AsType[gclient.CredentialNotFoundError](err); ok {
			return "", nil
		}
		return "", err
	}
	return credential.Secrets[credentialKey], nil
}

// Enabled reports whether requests are sent anywhere.
func (n *Notifier) Enabled(ctx context.Context) bool {
	url, err := n.webhookURL(ctx)
	return err == nil && url != ""
}

// Settings reports where the webhook stands.
func (n *Notifier) Settings(ctx context.Context) (Settings, error) {
	url, err := n.webhookURL(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		SlackWebhookConfigured: url != "",
		SlackWebhookHint:       webhookHint(url),
		SetViaDeployment:       n != nil && n.deployedURL != "",
	}, nil
}

// SetWebhook saves the webhook an administrator entered. An empty URL removes it.
func (n *Notifier) SetWebhook(ctx context.Context, url string) error {
	if n.deployedURL != "" {
		return ErrSetViaDeployment
	}
	if n.store == nil {
		return errors.New("no store for the Slack webhook")
	}
	url = strings.TrimSpace(url)
	if url == "" {
		_, err := n.store.DeleteCredential(ctx, credentialContext, credentialName)
		return err
	}
	if err := ValidateWebhookURL(url); err != nil {
		return err
	}
	return n.store.UpsertCredential(ctx, gtypes.Credential{
		Context: credentialContext,
		Name:    credentialName,
		Secrets: map[string]string{credentialKey: url},
	})
}

// ValidateWebhookURL checks that url can be posted to.
func ValidateWebhookURL(url string) error {
	parsed, err := neturl.Parse(url)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("enter the full webhook URL, starting with https://")
	}
	return nil
}

// webhookHint shows the start of the webhook's host and the last few characters, so an
// administrator can tell which webhook is set without exposing it.
func webhookHint(url string) string {
	if url == "" {
		return ""
	}
	parsed, err := neturl.Parse(url)
	if err != nil || parsed.Host == "" {
		return "…"
	}
	tail := url
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return parsed.Scheme + "://" + parsed.Host + "/…" + tail
}

// SendTest posts a test message, so an administrator can check the webhook.
func (n *Notifier) SendTest(ctx context.Context) error {
	url, err := n.webhookURL(ctx)
	if err != nil {
		return err
	}
	if url == "" {
		return errors.New("no Slack webhook is set")
	}
	return n.post(ctx, url, map[string]any{
		"text": "Obot will post new MCP server requests here.",
		"blocks": []any{map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": ":white_check_mark: Obot will post new MCP server requests to this channel."},
		}},
	})
}

// ReviewURL is the page where an administrator approves or denies the request.
func (n *Notifier) ReviewURL(id string) string {
	if n == nil {
		return "/mcp-server-requests/" + id
	}
	return n.baseURL + "/mcp-server-requests/" + id
}

// NotifyRequested alerts administrators that a request needs a decision.
func (n *Notifier) NotifyRequested(ctx context.Context, request types.MCPServerRequest) error {
	url, err := n.webhookURL(ctx)
	if err != nil || url == "" {
		return err
	}
	return n.post(ctx, url, slackMessage(request, n.ReviewURL(request.ID)))
}

func (n *Notifier) post(ctx context.Context, url string, message map[string]any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("post to Slack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("post to Slack: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// slackEscape escapes the characters Slack's mrkdwn treats as control characters.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func requestTarget(m types.MCPServerRequestManifest) string {
	switch m.Runtime {
	case types.RuntimeRemote:
		return m.RemoteURL
	case types.RuntimeNPX, types.RuntimeUVX:
		return fmt.Sprintf("%s %s", m.Runtime, m.Package)
	case types.RuntimeContainerized:
		return m.Image
	}
	return string(m.Runtime)
}

func slackMessage(r types.MCPServerRequest, reviewURL string) map[string]any {
	requester := r.RequesterName
	if r.RequesterEmail != "" && r.RequesterEmail != requester {
		requester = fmt.Sprintf("%s (%s)", cmpOr(requester, r.RequesterEmail), r.RequesterEmail)
	}
	requester = cmpOr(requester, r.RequesterEmail, r.RequesterID)

	fields := []map[string]any{
		{"type": "mrkdwn", "text": "*Server*\n" + slackEscape(r.Name)},
		{"type": "mrkdwn", "text": "*Requested by*\n" + slackEscape(requester)},
		{"type": "mrkdwn", "text": "*Runs as*\n`" + slackEscape(requestTarget(r.MCPServerRequestManifest)) + "`"},
	}
	if r.SourceURL != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Source*\n" + slackEscape(r.SourceURL)})
	}
	if id := cmpOr(r.ExistingCatalogEntryID, r.MatchedCatalogEntryID); id != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Already in the catalog*\n`" + slackEscape(id) + "`"})
	}

	blocks := []map[string]any{
		{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": fmt.Sprintf(":inbox_tray: *New MCP server request:* %s", slackEscape(r.Name))}},
		{"type": "section", "fields": fields},
	}
	if r.Reason != "" {
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "*Why*\n>" + strings.ReplaceAll(slackEscape(r.Reason), "\n", "\n>")}})
	}
	blocks = append(blocks, map[string]any{
		"type": "actions",
		"elements": []map[string]any{{
			"type":  "button",
			"style": "primary",
			"text":  map[string]any{"type": "plain_text", "text": "Review request"},
			"url":   reviewURL,
		}},
	})
	return map[string]any{
		// The text is the notification's fallback and preview.
		"text":   fmt.Sprintf("New MCP server request from %s: %s — review it at %s", requester, r.Name, reviewURL),
		"blocks": blocks,
	}
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
