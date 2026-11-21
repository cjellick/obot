package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	"github.com/obot-platform/obot/pkg/safehttp"
)

const (
	oktaPageSize   = 200
	oktaAppActive  = "ACTIVE"
	oktaAuthScheme = "SSWS "
)

var (
	// oktaNextLink picks the next page out of Okta's Link response header.
	oktaNextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)
)

// Okta lists the applications in an Okta organization, recognizes the AI tools among them,
// and counts who is assigned to each.
type Okta struct {
	// OrgURL is the organization's address, such as https://example.okta.com.
	OrgURL   string
	APIToken string
	Client   *http.Client
	// Classify optionally asks a model which of the application names the built-in list
	// didn't recognize are AI tools.
	Classify func(ctx context.Context, labels []string) map[string]riskregister.AIApp
}

type oktaApp struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

func (*Okta) Kind() types.RiskSourceKind {
	return types.RiskSourceOkta
}

func (o *Okta) Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error) {
	base := strings.TrimRight(o.OrgURL, "/")
	var apps []oktaApp
	if err := o.list(ctx, fmt.Sprintf("%s/api/v1/apps?limit=%d", base, oktaPageSize), func(page json.RawMessage) error {
		var batch []oktaApp
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		apps = append(apps, batch...)
		return nil
	}); err != nil {
		return nil, err
	}

	matches := make([]*riskregister.AIApp, len(apps))
	var unknown []string
	for i, app := range apps {
		if matches[i] = riskregister.MatchApp(app.Label); matches[i] == nil && app.Status == oktaAppActive {
			unknown = append(unknown, app.Label)
		}
	}
	if o.Classify != nil && len(unknown) > 0 {
		classified := o.Classify(ctx, unknown)
		for i, app := range apps {
			if match, ok := classified[app.Label]; ok && matches[i] == nil {
				match.ByModel = true
				matches[i] = &match
			}
		}
	}

	recognized := 0
	for i, app := range apps {
		if matches[i] == nil {
			continue
		}
		recognized++
		observation := riskregister.AppObservation{
			Source: types.RiskSourceOkta,
			ID:     app.ID,
			Label:  app.Label,
			Active: app.Status == oktaAppActive,
			Match:  matches[i],
		}
		if observation.Active {
			if err := o.list(ctx, fmt.Sprintf("%s/api/v1/apps/%s/users?limit=%d", base, url.PathEscape(app.ID), oktaPageSize), func(page json.RawMessage) error {
				var users []struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(page, &users); err != nil {
					return err
				}
				observation.AssignedUsers += len(users)
				return nil
			}); err != nil {
				return nil, err
			}
		}
		snapshot.Apps = append(snapshot.Apps, observation)
	}

	return map[string]int{"apps": len(apps), "aiApps": recognized}, nil
}

// list follows Okta's Link-header pagination from first, handing each page to visit. It only
// follows links that stay on the organization's own host.
func (o *Okta) list(ctx context.Context, first string, visit func(json.RawMessage) error) error {
	client := o.Client
	if client == nil {
		client = safehttp.NewClient(outsidePolicy)
	}
	origin, err := url.Parse(o.OrgURL)
	if err != nil {
		return fmt.Errorf("invalid Okta organization URL: %w", err)
	}

	next := first
	for page := 0; next != "" && page < maxPages; page++ {
		target, err := url.Parse(next)
		if err != nil || target.Host != origin.Host || target.Scheme != origin.Scheme {
			return fmt.Errorf("Okta returned a next page outside the organization: %q", next)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", oktaAuthScheme+o.APIToken)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			err := statusError("Okta", resp)
			resp.Body.Close()
			return err
		}
		var body json.RawMessage
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read the Okta response: %w", err)
		}
		if err := visit(body); err != nil {
			return fmt.Errorf("failed to read the Okta response: %w", err)
		}

		next = ""
		for _, link := range resp.Header.Values("Link") {
			if match := oktaNextLink.FindStringSubmatch(link); match != nil {
				next = match[1]
			}
		}
	}
	return nil
}
