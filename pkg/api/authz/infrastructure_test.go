package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/obot-platform/obot/apiclient/types"
	"k8s.io/apiserver/pkg/authentication/user"
)

// Infrastructure, including Obot's own logs, is for administrators only.
func TestInfrastructureAuthorization(t *testing.T) {
	authorizer := NewAuthorizer(nil, nil, nil, false, nil, nil, nil)
	tests := []struct {
		name    string
		role    types.Role
		allowed bool
	}{
		{
			name:    "owner",
			role:    types.RoleOwner,
			allowed: true,
		},
		{
			name:    "admin",
			role:    types.RoleAdmin,
			allowed: true,
		},
		{
			name: "auditor",
			role: types.RoleAuditor,
		},
		{
			name: "basic user",
			role: types.RoleBasic,
		},
	}
	paths := []string{
		"/api/infrastructure",
		"/api/infrastructure/logs",
		"/api/infrastructure/mcp-servers/ms1abc",
		"/api/infrastructure/mcp-servers/ms1abc/logs",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &user.DefaultInfo{Name: tt.name, Groups: tt.role.Groups()}
			for _, path := range paths {
				if got := authorizer.Authorize(httptest.NewRequest(http.MethodGet, path, nil), info); got != tt.allowed {
					t.Errorf("GET %s allowed = %t, want %t", path, got, tt.allowed)
				}
			}
			if authorizer.Authorize(httptest.NewRequest(http.MethodDelete, "/api/infrastructure/logs", nil), info) {
				t.Error("DELETE allowed, want it refused")
			}
		})
	}
}
