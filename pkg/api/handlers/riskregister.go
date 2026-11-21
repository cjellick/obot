package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/git"
	"github.com/obot-platform/obot/pkg/riskregister/collect"
	"github.com/obot-platform/obot/pkg/safehttp"
)

const (
	riskRunTriggerManual = "manual"
	riskRunListLimit     = 20
)

var (
	// riskSourceFields lists, for each source an administrator configures, the settings
	// and secrets it accepts. Obot's own data and device scans need no configuration.
	riskSourceFields = map[types.RiskSourceKind]riskSourceField{
		types.RiskSourceOkta:             {Config: []string{collect.ConfigOktaOrgURL}, Secrets: []string{collect.SecretOktaAPIToken}},
		types.RiskSourceClaudeCompliance: {Secrets: []string{collect.SecretComplianceAPIKey}},
		types.RiskSourceAnthropicAdmin:   {Secrets: []string{collect.SecretAnthropicAdminKey}},
		types.RiskSourceRepositories:     {Config: []string{collect.ConfigRepositoryURLs, collect.ConfigGitCredentialID}},
	}

	// riskSourceOrder is the order sources are listed in.
	riskSourceOrder = []types.RiskSourceKind{
		types.RiskSourceObot,
		types.RiskSourceSentry,
		types.RiskSourceOkta,
		types.RiskSourceClaudeCompliance,
		types.RiskSourceAnthropicAdmin,
		types.RiskSourceRepositories,
	}

	// riskSourcePolicy is the egress policy source addresses are checked against on save.
	riskSourcePolicy = safehttp.Options{BlockLoopback: true, BlockLinkLocal: true, BlockPrivateIP: true}
)

// RiskRegisterHandler serves the AI risk register: its entries, the decisions people record
// on them, the sources it collects from, and collection runs.
type RiskRegisterHandler struct {
	runner *collect.Runner
}

type riskSourceField struct {
	Config  []string
	Secrets []string
}

func NewRiskRegisterHandler(runner *collect.Runner) *RiskRegisterHandler {
	return &RiskRegisterHandler{runner: runner}
}

func (h *RiskRegisterHandler) ListRisks(req api.Context) error {
	query := req.URL.Query()
	risks, err := req.GatewayClient.ListRisks(req.Context(), gclient.RiskListOptions{
		Status:  query["status"],
		Segment: query["segment"],
		Type:    query["type"],
	})
	if err != nil {
		return err
	}
	items := make([]types.Risk, 0, len(risks))
	for _, risk := range risks {
		items = append(items, convertRisk(risk, nil))
	}
	return req.Write(types.RiskList{Items: items})
}

func (h *RiskRegisterHandler) GetRisk(req api.Context) error {
	id, err := parsePathUint(req, "risk_id", "risk id")
	if err != nil {
		return err
	}
	risk, decisions, err := req.GatewayClient.GetRisk(req.Context(), id)
	if gclient.ErrRiskNotFound(err) {
		return types.NewErrNotFound("risk %d not found", id)
	} else if err != nil {
		return err
	}
	return req.Write(convertRisk(*risk, decisions))
}

// Decide records what a person decided about a risk. Accepting or dismissing a risk needs a
// reason, and marking one treated needs the policy that treats it, because the register is
// only useful as a record if it says why.
func (h *RiskRegisterHandler) Decide(req api.Context) error {
	id, err := parsePathUint(req, "risk_id", "risk id")
	if err != nil {
		return err
	}
	var body types.RiskDecisionRequest
	if err := req.Read(&body); err != nil {
		return err
	}
	body.Note, body.Owner, body.TreatmentRef = strings.TrimSpace(body.Note), strings.TrimSpace(body.Owner), strings.TrimSpace(body.TreatmentRef)
	switch body.Action {
	case types.RiskDecisionAccept, types.RiskDecisionDismiss, types.RiskDecisionComment:
		if body.Note == "" {
			return types.NewErrBadRequest("a note is required to %s a risk", body.Action)
		}
	case types.RiskDecisionTreat:
		if body.TreatmentRef == "" {
			return types.NewErrBadRequest("treatmentRef is required: name the policy that treats the risk")
		}
	case types.RiskDecisionAssign:
		if body.Owner == "" {
			return types.NewErrBadRequest("owner is required")
		}
	case types.RiskDecisionReopen:
	default:
		return types.NewErrBadRequest("unknown action %q", body.Action)
	}

	risk, err := req.GatewayClient.DecideRisk(req.Context(), id, time.Now().UTC(), req.User.GetUID(), req.User.GetName(), body)
	if gclient.ErrRiskNotFound(err) {
		return types.NewErrNotFound("risk %d not found", id)
	} else if err != nil {
		return err
	}
	_, decisions, err := req.GatewayClient.GetRisk(req.Context(), id)
	if err != nil {
		return err
	}
	return req.Write(convertRisk(*risk, decisions))
}

// ListSources returns every source the register can collect from, configured or not, with
// how each did in the latest run.
func (h *RiskRegisterHandler) ListSources(req api.Context) error {
	stored, err := req.GatewayClient.ListRiskSources(req.Context())
	if err != nil {
		return err
	}
	byKind := make(map[types.RiskSourceKind]gtypes.RiskSource, len(stored))
	for _, source := range stored {
		byKind[types.RiskSourceKind(source.Kind)] = source
	}

	items := make([]types.RiskSource, 0, len(riskSourceOrder))
	for _, kind := range riskSourceOrder {
		source, err := h.source(req, kind, byKind[kind])
		if err != nil {
			return err
		}
		items = append(items, source)
	}
	return req.Write(types.RiskSourceList{Items: items})
}

// UpdateSource configures one of the sources outside Obot.
func (h *RiskRegisterHandler) UpdateSource(req api.Context) error {
	kind := types.RiskSourceKind(req.PathValue("kind"))
	fields, ok := riskSourceFields[kind]
	if !ok {
		return types.NewErrBadRequest("source %q can't be configured", kind)
	}
	var body types.RiskSourceRequest
	if err := req.Read(&body); err != nil {
		return err
	}

	config := map[string]string{}
	for _, name := range fields.Config {
		config[name] = strings.TrimSpace(body.Config[name])
	}
	secrets := map[string]string{}
	for _, name := range fields.Secrets {
		secrets[name] = strings.TrimSpace(body.Secrets[name])
	}
	if err := validateRiskSource(req, kind, config); err != nil {
		return err
	}

	stored := gtypes.RiskSource{Kind: string(kind), UpdatedAt: time.Now().UTC(), Enabled: body.Enabled, Config: config}
	if err := req.GatewayClient.SaveRiskSource(req.Context(), stored, secrets); err != nil {
		return err
	}
	source, err := h.source(req, kind, stored)
	if err != nil {
		return err
	}
	return req.Write(source)
}

// StartRun begins a collection run. The run carries on after the request returns.
func (h *RiskRegisterHandler) StartRun(req api.Context) error {
	run, err := h.runner.Start(req.Context(), riskRunTriggerManual)
	if errors.Is(err, collect.ErrRunInProgress) {
		return types.NewErrHTTP(http.StatusConflict, err.Error())
	} else if err != nil {
		return err
	}
	return req.WriteCode(convertRiskRun(*run), http.StatusAccepted)
}

func (h *RiskRegisterHandler) ListRuns(req api.Context) error {
	runs, err := req.GatewayClient.ListRiskRuns(req.Context(), riskRunListLimit)
	if err != nil {
		return err
	}
	items := make([]types.RiskCollectionRun, 0, len(runs))
	for _, run := range runs {
		items = append(items, convertRiskRun(run))
	}
	return req.Write(types.List[types.RiskCollectionRun]{Items: items})
}

// GetProfile returns who uses AI and how, from the latest finished run. It is empty until a
// run has finished.
func (h *RiskRegisterHandler) GetProfile(req api.Context) error {
	run, err := req.GatewayClient.LatestRiskRun(req.Context())
	if err != nil {
		return err
	}
	if run == nil || run.Profile == nil {
		return req.Write(types.RiskUsageProfile{Segments: []types.RiskUsageSegment{}})
	}
	return req.Write(*run.Profile)
}

// source builds the API form of a source: its settings, which secrets are stored (never
// their values), and its latest status.
func (h *RiskRegisterHandler) source(req api.Context, kind types.RiskSourceKind, stored gtypes.RiskSource) (types.RiskSource, error) {
	source := types.RiskSource{Kind: kind, Enabled: stored.Enabled, Config: stored.Config, Status: stored.LastStatus}
	fields, configurable := riskSourceFields[kind]
	if !configurable {
		// Obot's own data and device scans are always collected.
		source.Enabled, source.Config = true, nil
		return source, nil
	}
	secrets, err := req.GatewayClient.RiskSourceSecrets(req.Context(), string(kind))
	if err != nil {
		return source, err
	}
	for _, name := range fields.Secrets {
		if secrets[name] != "" {
			source.ConfiguredSecrets = append(source.ConfiguredSecrets, name)
		}
	}
	sort.Strings(source.ConfiguredSecrets)
	return source, nil
}

// validateRiskSource checks the addresses in a source's settings before they are stored, so
// a source can't be pointed at an internal address.
func validateRiskSource(req api.Context, kind types.RiskSourceKind, config map[string]string) error {
	switch kind {
	case types.RiskSourceOkta:
		orgURL := config[collect.ConfigOktaOrgURL]
		if orgURL == "" {
			return nil
		}
		parsed, err := url.Parse(orgURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" {
			return types.NewErrBadRequest("orgURL must be an https address with no path, such as https://example.okta.com")
		}
		if err := safehttp.ValidateURL(req.Context(), orgURL, riskSourcePolicy); err != nil {
			if _, blocked := errors.AsType[*safehttp.BlockedAddressError](err); blocked {
				return types.NewErrBadRequest("orgURL is not allowed: %v", err)
			}
			return types.NewErrBadRequest("orgURL could not be checked: %v", err)
		}
	case types.RiskSourceRepositories:
		for _, repoURL := range collect.SplitList(config[collect.ConfigRepositoryURLs]) {
			if !strings.HasPrefix(repoURL, "https://") {
				return types.NewErrBadRequest("repository %q must be an https address", repoURL)
			}
			if _, err := git.NormalizeRepositoryURL(repoURL); err != nil {
				return types.NewErrBadRequest("repository %q is not valid: %v", repoURL, err)
			}
		}
	}
	return nil
}

func convertRisk(risk gtypes.Risk, decisions []gtypes.RiskDecision) types.Risk {
	converted := types.Risk{
		ID:           risk.ID,
		Created:      *types.NewTime(risk.CreatedAt),
		Type:         risk.Type,
		Segment:      types.RiskSegment(risk.Segment),
		Severity:     types.RiskSeverity(risk.Severity),
		Status:       types.RiskStatus(risk.Status),
		Title:        risk.Title,
		Summary:      risk.Summary,
		Analysis:     risk.Analysis,
		Sources:      risk.Sources,
		Evidence:     risk.Evidence,
		Metrics:      risk.Metrics,
		Treatment:    risk.Treatment,
		Frameworks:   risk.Frameworks,
		Owner:        risk.Owner,
		FirstSeenAt:  *types.NewTime(risk.FirstSeenAt),
		LastSeenAt:   *types.NewTime(risk.LastSeenAt),
		Observed:     risk.Observed,
		TreatedAt:    optionalAPITime(risk.TreatedAt),
		TreatmentRef: risk.TreatmentRef,
		Proof:        risk.Proof,
	}
	for _, decision := range decisions {
		converted.Decisions = append(converted.Decisions, types.RiskDecision{
			ID:       decision.ID,
			Created:  *types.NewTime(decision.CreatedAt),
			UserID:   decision.UserID,
			Username: decision.Username,
			Action:   types.RiskDecisionAction(decision.Action),
			Note:     decision.Note,
			From:     types.RiskStatus(decision.FromStatus),
			To:       types.RiskStatus(decision.ToStatus),
		})
	}
	return converted
}

func convertRiskRun(run gtypes.RiskCollectionRun) types.RiskCollectionRun {
	return types.RiskCollectionRun{
		ID:         run.ID,
		StartedAt:  *types.NewTime(run.StartedAt),
		FinishedAt: optionalAPITime(run.FinishedAt),
		Trigger:    run.Trigger,
		Sources:    run.Sources,
		Profile:    run.Profile,
		Found:      run.Found,
		New:        run.New,
		Error:      run.Error,
		Analyzed:   run.Analyzed,
	}
}
