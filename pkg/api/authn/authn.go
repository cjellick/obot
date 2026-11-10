package authn

import (
	"context"
	"net/http"

	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
)

// internalUserKey marks a request Obot makes to its own API on a user's behalf. A context value
// can't be set from outside the process, so a request carrying one was made by Obot itself.
type internalUserKey struct{}

// WithInternalUser marks ctx as a request Obot makes to its own API as u, such as an Obot AI
// tool call. The request is authorized and audit-logged as u, as if u had made it.
func WithInternalUser(ctx context.Context, u user.Info) context.Context {
	return context.WithValue(ctx, internalUserKey{}, u)
}

type Authenticator struct {
	authenticator authenticator.Request
}

func NewAuthenticator(authenticator authenticator.Request) *Authenticator {
	return &Authenticator{
		authenticator: authenticator,
	}
}

func (a *Authenticator) Authenticate(req *http.Request) (user.Info, error) {
	if u, ok := req.Context().Value(internalUserKey{}).(user.Info); ok && u != nil {
		return u, nil
	}
	resp, ok, err := a.authenticator.AuthenticateRequest(req)
	if err != nil {
		return nil, err
	}
	if !ok {
		panic("authentication should always succeed")
	}
	return resp.User, nil
}
