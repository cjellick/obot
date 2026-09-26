package handlers

import (
	"testing"
)

func TestAllowedBifrostAPIPath(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		escaped string
		want    string
		wantOK  bool
	}{
		{
			name:    "provider list",
			raw:     "providers",
			escaped: "/api/llm-gateway/admin/providers",
			want:    "providers",
			wantOK:  true,
		},
		{
			name:    "provider keys",
			raw:     "providers/openai/keys",
			escaped: "/api/llm-gateway/admin/providers/openai/keys",
			want:    "providers/openai/keys",
			wantOK:  true,
		},
		{
			name:    "virtual keys",
			raw:     "governance/virtual-keys",
			escaped: "/api/llm-gateway/admin/governance/virtual-keys",
			want:    "governance/virtual-keys",
			wantOK:  true,
		},
		{
			name:    "hidden api",
			raw:     "mcp/clients",
			escaped: "/api/llm-gateway/admin/mcp/clients",
		},
		{
			name:    "dot segments",
			raw:     "providers/../session/login",
			escaped: "/api/llm-gateway/admin/providers/../session/login",
		},
		{
			name:    "encoded dot segments",
			raw:     "providers/../mcp/clients",
			escaped: "/api/llm-gateway/admin/providers/%2e%2e/mcp/clients",
		},
		{
			name:    "encoded slash",
			raw:     "providers/../mcp/clients",
			escaped: "/api/llm-gateway/admin/providers%2F..%2Fmcp%2Fclients",
		},
		{
			name:    "prefix lookalike",
			raw:     "providersx",
			escaped: "/api/llm-gateway/admin/providersx",
		},
		{
			name:    "double slash",
			raw:     "providers//openai",
			escaped: "/api/llm-gateway/admin/providers//openai",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := allowedBifrostAPIPath(tt.raw, tt.escaped)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRejectSecretReferences(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "plain key",
			body: `{"name":"k","value":"sk-123","models":["*"]}`,
		},
		{
			name:    "env reference",
			body:    `{"name":"k","value":"env.OBOT_SERVER_DSN"}`,
			wantErr: true,
		},
		{
			name:    "nested vault reference",
			body:    `{"azure_key_config":{"endpoint":"vault.secret/path"}}`,
			wantErr: true,
		},
		{
			name:    "object env reference",
			body:    `{"value":{"ref":"HOME","type":"env"}}`,
			wantErr: true,
		},
		{
			name:    "from_env flag",
			body:    `{"value":{"env_var":"HOME","from_env":true}}`,
			wantErr: true,
		},
		{
			name:    "reference in array",
			body:    `{"headers":["env.SECRET"]}`,
			wantErr: true,
		},
		{
			name: "not json",
			body: `hello`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectSecretReferences([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
