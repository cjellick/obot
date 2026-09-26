package bifrost

import (
	"context"
	"log/slog"
	"time"

	gateway "github.com/obot-platform/obot/pkg/gateway/client"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// legacyImportAnnotation marks the gateway's ModelProvider once legacy provider
	// credentials have been imported, so deleting imported providers later doesn't bring
	// them back.
	legacyImportAnnotation = "obot.ai/llm-gateway-legacy-import"
)

// TriggerModelSync flips the sync annotation on the gateway's ModelProvider so
// the provider controller re-lists models.
func TriggerModelSync(ctx context.Context, c kclient.Client) error {
	var mp v1.ModelProvider
	if err := c.Get(ctx, kclient.ObjectKey{Namespace: system.DefaultNamespace, Name: system.LLMGatewayModelProvider}, &mp); err != nil {
		return err
	}
	if mp.Annotations[v1.ModelProviderSyncAnnotation] == "" {
		if mp.Annotations == nil {
			mp.Annotations = map[string]string{}
		}
		mp.Annotations[v1.ModelProviderSyncAnnotation] = "true"
	} else {
		delete(mp.Annotations, v1.ModelProviderSyncAnnotation)
	}
	return c.Update(ctx, &mp)
}

// RunStartupTasks waits for the engine to start, imports legacy model provider credentials
// the first time the gateway starts without any providers, and then keeps Obot's model
// list in step with Bifrost's.
func (e *Engine) RunStartupTasks(ctx context.Context, c kclient.Client, gatewayClient *gateway.Client) {
	if err := e.Wait(ctx); err != nil {
		return
	}

	var mp v1.ModelProvider
	for {
		err := c.Get(ctx, kclient.ObjectKey{Namespace: system.DefaultNamespace, Name: system.LLMGatewayModelProvider}, &mp)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}

	if mp.Annotations[legacyImportAnnotation] == "" && !e.HasUpstreamProviders(ctx) {
		results, err := e.ImportLegacyModelProviders(ctx, c, gatewayClient)
		failed := err != nil
		if err != nil {
			slog.Warn("failed to import legacy model providers into the LLM gateway", "error", err)
		}
		for _, r := range results {
			slog.Info("LLM gateway legacy provider import", "modelProvider", r.ModelProvider, "gatewayProvider", r.GatewayProvider, "status", r.Status, "message", r.Message)
			failed = failed || r.Status == "error"
		}
		// Only record the import once it fully succeeded, so a failure is retried next start.
		if !failed {
			if err := c.Get(ctx, kclient.ObjectKeyFromObject(&mp), &mp); err == nil {
				if mp.Annotations == nil {
					mp.Annotations = map[string]string{}
				}
				mp.Annotations[legacyImportAnnotation] = "true"
				if err := c.Update(ctx, &mp); err != nil {
					slog.Warn("failed to record LLM gateway legacy import", "error", err)
				}
			}
		}
	}

	e.ApplyEgressProxy(ctx)

	// Bifrost refreshes provider model lists on its own schedule, so resync periodically.
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		// A conflicting write just means the provider changed underneath; retry once fresh.
		if err := TriggerModelSync(ctx, c); err != nil {
			if err := TriggerModelSync(ctx, c); err != nil {
				slog.Warn("failed to trigger LLM gateway model sync", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
