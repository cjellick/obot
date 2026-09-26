package hostedagentmodels

import (
	"slices"
	"testing"

	"github.com/obot-platform/obot/pkg/system"
)

func TestLLMGatewayModelsAcceptEveryAPI(t *testing.T) {
	apis := APIsFor(system.BifrostModelProvider, "AnthropicMessages")
	for _, want := range []string{APIAnthropic, APIOpenAIResponses, APIOpenAIChatCompletions} {
		if !slices.Contains(apis, want) {
			t.Errorf("expected %q in %v", want, apis)
		}
	}

	path, ok := ProxyPath(system.BifrostModelProvider)
	if !ok || path != "bifrost" {
		t.Fatalf("ProxyPath = %q, %v", path, ok)
	}
}
