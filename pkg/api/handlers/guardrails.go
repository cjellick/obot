package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	gatewayclient "github.com/obot-platform/obot/pkg/gateway/client"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/guardrails"
	"gorm.io/gorm"
)

// GuardrailsHandler manages guardrail rules and profiles, and lists guardrail events.
type GuardrailsHandler struct {
	engine *guardrails.Engine
}

// GuardrailProfileResponse is a profile as the API returns it: sensitive settings are
// removed from its configuration and only named in ConfiguredSecrets.
type GuardrailProfileResponse struct {
	gatewaytypes.GuardrailProfile
	ConfiguredSecrets []string `json:"configuredSecrets"`
	// Error explains why the profile can't be used, such as a missing required setting.
	Error string `json:"error,omitempty"`
}

type guardrailEventList struct {
	Items []gatewaytypes.GuardrailEvent `json:"items"`
	Total int64                         `json:"total"`
}

type guardrailConditionRequest struct {
	Target    string `json:"target"`
	Condition string `json:"condition"`
}

type guardrailTestRequest struct {
	Target string `json:"target"`
	Stage  string `json:"stage"`
	Text   string `json:"text"`
}

type guardrailTestResponse struct {
	Findings     []guardrails.Finding `json:"findings"`
	RedactedText string               `json:"redactedText,omitempty"`
	CanRedact    bool                 `json:"canRedact"`
	Error        string               `json:"error,omitempty"`
}

func NewGuardrailsHandler(engine *guardrails.Engine) *GuardrailsHandler {
	return &GuardrailsHandler{engine: engine}
}

func (h *GuardrailsHandler) invalidate() {
	if h.engine != nil {
		h.engine.Invalidate()
	}
}

// Providers lists the available profile types and their settings.
func (h *GuardrailsHandler) Providers(req api.Context) error {
	return req.Write(map[string]any{"items": guardrails.Providers()})
}

func (h *GuardrailsHandler) ListRules(req api.Context) error {
	rules, err := req.GatewayClient.ListGuardrailRules(req.Context())
	if err != nil {
		return err
	}
	return req.Write(map[string]any{"items": rules})
}

func (h *GuardrailsHandler) GetRule(req api.Context) error {
	rule, err := getGuardrailRule(req)
	if err != nil {
		return err
	}
	return req.Write(rule)
}

func (h *GuardrailsHandler) CreateRule(req api.Context) error {
	var rule gatewaytypes.GuardrailRule
	if err := req.Read(&rule); err != nil {
		return types.NewErrBadRequest("invalid rule: %v", err)
	}
	rule.ID = ""
	if err := validateGuardrailRule(req, &rule); err != nil {
		return err
	}
	if err := req.GatewayClient.CreateGuardrailRule(req.Context(), &rule); err != nil {
		return err
	}
	h.invalidate()
	return req.WriteCreated(rule)
}

func (h *GuardrailsHandler) UpdateRule(req api.Context) error {
	existing, err := getGuardrailRule(req)
	if err != nil {
		return err
	}
	var rule gatewaytypes.GuardrailRule
	if err := req.Read(&rule); err != nil {
		return types.NewErrBadRequest("invalid rule: %v", err)
	}
	rule.ID = existing.ID
	if err := validateGuardrailRule(req, &rule); err != nil {
		return err
	}
	if err := req.GatewayClient.UpdateGuardrailRule(req.Context(), &rule); err != nil {
		return err
	}
	h.invalidate()
	updated, err := getGuardrailRule(req)
	if err != nil {
		return err
	}
	return req.Write(updated)
}

func (h *GuardrailsHandler) DeleteRule(req api.Context) error {
	if _, err := getGuardrailRule(req); err != nil {
		return err
	}
	if err := req.GatewayClient.DeleteGuardrailRule(req.Context(), req.PathValue("rule_id")); err != nil {
		return err
	}
	h.invalidate()
	req.WriteHeader(http.StatusNoContent)
	return nil
}

// ValidateCondition compiles a rule condition, so the UI can report errors as it's typed.
func (h *GuardrailsHandler) ValidateCondition(req api.Context) error {
	var in guardrailConditionRequest
	if err := req.Read(&in); err != nil {
		return types.NewErrBadRequest("invalid request: %v", err)
	}
	if in.Target != gatewaytypes.GuardrailTargetLLM && in.Target != gatewaytypes.GuardrailTargetMCP {
		return types.NewErrBadRequest("target must be llm or mcp")
	}
	result := map[string]any{"valid": true}
	if strings.TrimSpace(in.Condition) != "" {
		if _, err := guardrails.CompileCondition(in.Target, in.Condition); err != nil {
			result = map[string]any{"valid": false, "error": err.Error()}
		}
	}
	return req.Write(result)
}

func (h *GuardrailsHandler) ListProfiles(req api.Context) error {
	profiles, err := req.GatewayClient.ListGuardrailProfiles(req.Context())
	if err != nil {
		return err
	}
	items := make([]GuardrailProfileResponse, 0, len(profiles))
	for _, p := range profiles {
		resp, err := profileResponse(req, p)
		if err != nil {
			return err
		}
		items = append(items, resp)
	}
	return req.Write(map[string]any{"items": items})
}

func (h *GuardrailsHandler) GetProfile(req api.Context) error {
	profile, err := getGuardrailProfile(req)
	if err != nil {
		return err
	}
	resp, err := profileResponse(req, *profile)
	if err != nil {
		return err
	}
	return req.Write(resp)
}

func (h *GuardrailsHandler) CreateProfile(req api.Context) error {
	var profile gatewaytypes.GuardrailProfile
	if err := req.Read(&profile); err != nil {
		return types.NewErrBadRequest("invalid profile: %v", err)
	}
	profile.ID = ""
	secrets, err := splitProfileSecrets(&profile, nil)
	if err != nil {
		return err
	}
	if err := req.GatewayClient.CreateGuardrailProfile(req.Context(), &profile, secrets); err != nil {
		return err
	}
	h.invalidate()
	resp, err := profileResponse(req, profile)
	if err != nil {
		return err
	}
	return req.WriteCreated(resp)
}

// UpdateProfile saves a profile. Sensitive settings that are omitted or empty keep their
// current values.
func (h *GuardrailsHandler) UpdateProfile(req api.Context) error {
	existing, err := getGuardrailProfile(req)
	if err != nil {
		return err
	}
	var profile gatewaytypes.GuardrailProfile
	if err := req.Read(&profile); err != nil {
		return types.NewErrBadRequest("invalid profile: %v", err)
	}
	profile.ID = existing.ID
	if profile.Type != "" && profile.Type != existing.Type {
		return types.NewErrBadRequest("a profile's type can't be changed")
	}
	profile.Type = existing.Type
	current, err := req.GatewayClient.GuardrailProfileSecrets(req.Context(), existing.ID)
	if err != nil {
		return err
	}
	if err := checkDestinationChange(existing, &profile, current); err != nil {
		return err
	}
	secrets, err := splitProfileSecrets(&profile, current)
	if err != nil {
		return err
	}
	if err := req.GatewayClient.UpdateGuardrailProfile(req.Context(), &profile); err != nil {
		return err
	}
	if err := req.GatewayClient.SetGuardrailProfileSecrets(req.Context(), existing.ID, secrets); err != nil {
		return err
	}
	h.invalidate()
	updated, err := getGuardrailProfile(req)
	if err != nil {
		return err
	}
	resp, err := profileResponse(req, *updated)
	if err != nil {
		return err
	}
	return req.Write(resp)
}

func (h *GuardrailsHandler) DeleteProfile(req api.Context) error {
	profile, err := getGuardrailProfile(req)
	if err != nil {
		return err
	}
	rules, err := req.GatewayClient.ListGuardrailRules(req.Context())
	if err != nil {
		return err
	}
	for _, r := range rules {
		if slices.Contains(r.ProfileIDs, profile.ID) {
			return types.NewErrHTTP(http.StatusConflict, fmt.Sprintf("profile is used by rule %q; remove it from the rule first", r.Name))
		}
	}
	if err := req.GatewayClient.DeleteGuardrailProfile(req.Context(), profile.ID); err != nil {
		return err
	}
	h.invalidate()
	req.WriteHeader(http.StatusNoContent)
	return nil
}

// TestProfile runs a saved profile against sample text.
func (h *GuardrailsHandler) TestProfile(req api.Context) error {
	if h.engine == nil {
		return types.NewErrHTTP(http.StatusServiceUnavailable, "guardrails are not available")
	}
	profile, err := getGuardrailProfile(req)
	if err != nil {
		return err
	}
	var in guardrailTestRequest
	if err := req.Read(&in); err != nil {
		return types.NewErrBadRequest("invalid request: %v", err)
	}
	if in.Target == "" {
		in.Target = gatewaytypes.GuardrailTargetLLM
	}
	if in.Stage == "" {
		in.Stage = guardrails.StageInput
	}
	secrets, err := req.GatewayClient.GuardrailProfileSecrets(req.Context(), profile.ID)
	if err != nil {
		return err
	}
	result, redacted, err := h.engine.TestProfile(req.Context(), *profile, secrets, in.Target, in.Stage, in.Text)
	if err != nil {
		return req.Write(guardrailTestResponse{Findings: []guardrails.Finding{}, Error: err.Error()})
	}
	resp := guardrailTestResponse{Findings: result.Findings, CanRedact: redacted != nil}
	if resp.Findings == nil {
		resp.Findings = []guardrails.Finding{}
	}
	if len(redacted) > 0 && len(result.Findings) > 0 {
		resp.RedactedText = redacted[0].Text
	}
	return req.Write(resp)
}

func (h *GuardrailsHandler) ListEvents(req api.Context) error {
	q := req.URL.Query()
	filter := gatewayclient.GuardrailEventFilter{
		RuleID:  q.Get("ruleID"),
		Outcome: q.Get("outcome"),
		UserID:  q.Get("userID"),
		Target:  q.Get("target"),
	}
	filter.Limit, _ = strconv.Atoi(q.Get("limit"))
	filter.Offset, _ = strconv.Atoi(q.Get("offset"))
	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return types.NewErrBadRequest("invalid since: %v", err)
		}
		filter.Since = t
	}
	events, total, err := req.GatewayClient.ListGuardrailEvents(req.Context(), filter)
	if err != nil {
		return err
	}
	if events == nil {
		events = []gatewaytypes.GuardrailEvent{}
	}
	return req.Write(guardrailEventList{Items: events, Total: total})
}

func getGuardrailRule(req api.Context) (*gatewaytypes.GuardrailRule, error) {
	rule, err := req.GatewayClient.GetGuardrailRule(req.Context(), req.PathValue("rule_id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.NewErrNotFound("guardrail rule %s not found", req.PathValue("rule_id"))
	}
	return rule, err
}

func getGuardrailProfile(req api.Context) (*gatewaytypes.GuardrailProfile, error) {
	profile, err := req.GatewayClient.GetGuardrailProfile(req.Context(), req.PathValue("profile_id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.NewErrNotFound("guardrail profile %s not found", req.PathValue("profile_id"))
	}
	return profile, err
}

func validateGuardrailRule(req api.Context, rule *gatewaytypes.GuardrailRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return types.NewErrBadRequest("name is required")
	}
	switch rule.Target {
	case gatewaytypes.GuardrailTargetLLM, gatewaytypes.GuardrailTargetMCP:
	default:
		return types.NewErrBadRequest("target must be llm or mcp")
	}
	switch rule.ApplyTo {
	case gatewaytypes.GuardrailApplyInput, gatewaytypes.GuardrailApplyOutput, gatewaytypes.GuardrailApplyBoth:
	case "":
		rule.ApplyTo = gatewaytypes.GuardrailApplyBoth
	default:
		return types.NewErrBadRequest("applyTo must be input, output or both")
	}
	switch rule.Action {
	case gatewaytypes.GuardrailActionBlock, gatewaytypes.GuardrailActionRedact, gatewaytypes.GuardrailActionLog:
	default:
		return types.NewErrBadRequest("action must be block, redact or log")
	}
	switch rule.InputScope {
	case gatewaytypes.GuardrailScopeAll, gatewaytypes.GuardrailScopeLatest:
	case "":
		rule.InputScope = gatewaytypes.GuardrailScopeLatest
	default:
		return types.NewErrBadRequest("inputScope must be all or latest")
	}
	if rule.SamplingRate == 0 {
		rule.SamplingRate = 100
	}
	if rule.SamplingRate < 1 || rule.SamplingRate > 100 {
		return types.NewErrBadRequest("samplingRate must be between 1 and 100")
	}
	if strings.TrimSpace(rule.Condition) != "" {
		if _, err := guardrails.CompileCondition(rule.Target, rule.Condition); err != nil {
			return types.NewErrBadRequest("invalid condition: %v", err)
		}
	}
	if len(rule.ProfileIDs) == 0 {
		return types.NewErrBadRequest("at least one profile is required")
	}
	for _, id := range rule.ProfileIDs {
		if _, err := req.GatewayClient.GetGuardrailProfile(req.Context(), id); errors.Is(err, gorm.ErrRecordNotFound) {
			return types.NewErrBadRequest("profile %s does not exist", id)
		} else if err != nil {
			return err
		}
	}
	if rule.Subjects == nil {
		rule.Subjects = []gatewaytypes.GuardrailRuleSubject{}
	}
	for _, s := range rule.Subjects {
		switch s.Type {
		case gatewaytypes.LLMGatewayBudgetSubjectEveryone:
		case gatewaytypes.LLMGatewayBudgetSubjectUser, gatewaytypes.LLMGatewayBudgetSubjectGroup:
			if s.ID == "" {
				return types.NewErrBadRequest("%s subjects need an id", s.Type)
			}
		default:
			return types.NewErrBadRequest("subject type must be user, group or everyone")
		}
	}
	return nil
}

// checkDestinationChange refuses an update that changes where a profile's credentials are
// sent (an endpoint, or a region in a host name) while keeping the stored credentials.
// Otherwise an admin could point the profile at their own server, run a test, and receive
// credentials the API never returns.
func checkDestinationChange(existing *gatewaytypes.GuardrailProfile, updated *gatewaytypes.GuardrailProfile, current map[string]string) error {
	info, ok := guardrails.Provider(existing.Type)
	if !ok || len(current) == 0 {
		return nil
	}
	oldConfig, newConfig := map[string]any{}, map[string]any{}
	_ = json.Unmarshal(existing.Config, &oldConfig)
	_ = json.Unmarshal(updated.Config, &newConfig)
	var changed, kept []string
	for _, f := range info.Fields {
		switch {
		case f.Destination && configString(oldConfig[f.Name]) != configString(newConfig[f.Name]):
			changed = append(changed, f.Label)
		case f.Sensitive && current[f.Name] != "" && configString(newConfig[f.Name]) == "":
			kept = append(kept, f.Label)
		}
	}
	if len(changed) == 0 || len(kept) == 0 {
		return nil
	}
	return types.NewErrBadRequest("changing %s requires re-entering %s", strings.Join(changed, " and "), strings.Join(kept, " and "))
}

func configString(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// splitProfileSecrets validates a profile and moves its sensitive settings out of its
// configuration. current holds the saved secrets, which omitted sensitive settings keep.
func splitProfileSecrets(profile *gatewaytypes.GuardrailProfile, current map[string]string) (map[string]string, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		return nil, types.NewErrBadRequest("name is required")
	}
	info, ok := guardrails.Provider(profile.Type)
	if !ok {
		return nil, types.NewErrBadRequest("unknown profile type %q", profile.Type)
	}
	config := map[string]any{}
	if len(profile.Config) > 0 && string(profile.Config) != "null" {
		if err := json.Unmarshal(profile.Config, &config); err != nil {
			return nil, types.NewErrBadRequest("config must be an object: %v", err)
		}
	}
	secrets := map[string]string{}
	for _, f := range info.Fields {
		if !f.Sensitive {
			continue
		}
		value, _ := config[f.Name].(string)
		delete(config, f.Name)
		if value == "" {
			value = current[f.Name]
		}
		if value != "" {
			secrets[f.Name] = value
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	profile.Config = raw
	return secrets, nil
}

func profileResponse(req api.Context, profile gatewaytypes.GuardrailProfile) (GuardrailProfileResponse, error) {
	resp := GuardrailProfileResponse{GuardrailProfile: profile, ConfiguredSecrets: []string{}}
	secrets, err := req.GatewayClient.GuardrailProfileSecrets(req.Context(), profile.ID)
	if err != nil {
		return resp, err
	}
	info, ok := guardrails.Provider(profile.Type)
	if !ok {
		resp.Error = fmt.Sprintf("unknown profile type %q", profile.Type)
		return resp, nil
	}
	config := map[string]any{}
	_ = json.Unmarshal(profile.Config, &config)
	for _, f := range info.Fields {
		if f.Sensitive {
			delete(config, f.Name)
			if secrets[f.Name] != "" {
				resp.ConfiguredSecrets = append(resp.ConfiguredSecrets, f.Name)
			} else if f.Required {
				resp.Error = f.Label + " is required"
			}
		} else if v, ok := config[f.Name]; f.Required && (!ok || v == nil || v == "") {
			resp.Error = f.Label + " is required"
		}
	}
	resp.Config, _ = json.Marshal(config)
	return resp, nil
}
