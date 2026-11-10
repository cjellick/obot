package builtinmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/api/authn"
)

// SetAPI gives the built-in servers Obot's own API, which their management tools call as the
// signed-in user. Going through the API means a tool can do exactly what the user could do in
// the UI, with the same validation, authorization and API audit log.
func (h *Handler) SetAPI(api http.Handler) {
	h.api = api
}

// apiError is a failed API call, with the message the API gave.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Obot's API answered %d: %s", e.Status, e.Message)
}

// callAPI calls Obot's API as req's user. body, when set, is sent as JSON; the response, when
// out is set, is decoded into it.
func (h *Handler) callAPI(ctx context.Context, req api.Context, method, path string, query url.Values, body, out any) error {
	if h.api == nil {
		return fmt.Errorf("this Obot server can't call its own API")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	target := "/api" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	r := httptest.NewRequestWithContext(authn.WithInternalUser(ctx, req.User), method, target, reader)
	r.Host = req.Request.Host
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "obot-ai")
	if lang := req.Request.Header.Get("Accept-Language"); lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.api.ServeHTTP(rec, r)
	if rec.Code < 200 || rec.Code > 299 {
		return &apiError{Status: rec.Code, Message: apiErrorMessage(rec.Body.Bytes())}
	}
	if out == nil || rec.Body.Len() == 0 {
		return nil
	}
	return json.Unmarshal(rec.Body.Bytes(), out)
}

// apiErrorMessage pulls the message out of an API error body, which is JSON or plain text.
func apiErrorMessage(body []byte) string {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if msg := cmpOr(parsed.Error, parsed.Message); msg != "" {
			return msg
		}
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}
