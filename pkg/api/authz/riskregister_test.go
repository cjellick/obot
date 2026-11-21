package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/obot-platform/obot/apiclient/types"
	"k8s.io/apiserver/pkg/authentication/user"
)

// TestRiskRegisterRouteAuthorization checks that administrators manage the risk register,
// auditors can read it, and nobody else can reach it.
func TestRiskRegisterRouteAuthorization(t *testing.T) {
	authorizer := NewAuthorizer(nil, nil, nil, false, nil, nil, nil)
	users := []struct {
		name      string
		info      user.Info
		canRead   bool
		canMutate bool
	}{
		{
			name:      "owner",
			info:      &user.DefaultInfo{Name: "owner", Groups: types.RoleOwner.Groups()},
			canRead:   true,
			canMutate: true,
		},
		{
			name:      "administrator",
			info:      &user.DefaultInfo{Name: "admin", Groups: types.RoleAdmin.Groups()},
			canRead:   true,
			canMutate: true,
		},
		{
			name:    "auditor",
			info:    &user.DefaultInfo{Name: "auditor", Groups: types.RoleAuditor.Groups()},
			canRead: true,
		},
		{
			name: "power user",
			info: &user.DefaultInfo{Name: "power-user", Groups: types.RolePowerUser.Groups()},
		},
		{
			name: "basic user",
			info: &user.DefaultInfo{Name: "basic", Groups: types.RoleBasic.Groups()},
		},
		{
			name: "enrolled device",
			info: &user.DefaultInfo{Name: "device:device-1", Groups: []string{types.GroupDeviceScans, types.GroupAuthenticated}},
		},
		{
			name: "unauthenticated user",
			info: &user.DefaultInfo{Name: "anonymous", Groups: []string{UnauthenticatedGroup}},
		},
	}
	routes := []struct {
		name     string
		method   string
		path     string
		readOnly bool
	}{
		{
			name:     "list risks",
			method:   http.MethodGet,
			path:     "/api/risk-register/risks",
			readOnly: true,
		},
		{
			name:     "get risk",
			method:   http.MethodGet,
			path:     "/api/risk-register/risks/1",
			readOnly: true,
		},
		{
			name:     "usage profile",
			method:   http.MethodGet,
			path:     "/api/risk-register/profile",
			readOnly: true,
		},
		{
			name:     "list sources",
			method:   http.MethodGet,
			path:     "/api/risk-register/sources",
			readOnly: true,
		},
		{
			name:   "decide on a risk",
			method: http.MethodPost,
			path:   "/api/risk-register/risks/1/decisions",
		},
		{
			name:   "configure a source",
			method: http.MethodPut,
			path:   "/api/risk-register/sources/okta",
		},
		{
			name:   "start a run",
			method: http.MethodPost,
			path:   "/api/risk-register/runs",
		},
	}

	for _, u := range users {
		for _, route := range routes {
			t.Run(u.name+" "+route.name, func(t *testing.T) {
				want := u.canMutate || (route.readOnly && u.canRead)
				req := httptest.NewRequest(route.method, route.path, nil)
				if got := authorizer.Authorize(req, u.info); got != want {
					t.Fatalf("Authorize(%s %s) = %v, want %v", route.method, route.path, got, want)
				}
			})
		}
	}
}
