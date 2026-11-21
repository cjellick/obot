package collect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// runTimeout bounds one collection run.
	runTimeout = 15 * time.Minute

	// Source configuration and secret names.
	ConfigOktaOrgURL        = "orgURL"
	ConfigRepositoryURLs    = "urls"
	ConfigGitCredentialID   = "gitCredentialID"
	SecretOktaAPIToken      = "apiToken"
	SecretComplianceAPIKey  = "apiKey"
	SecretAnthropicAdminKey = "adminKey"

	skippedNotConfigured = "Not configured."
	skippedDisabled      = "Turned off."
)

var (
	// ErrRunInProgress is returned when a collection run is started while one is running.
	ErrRunInProgress = errors.New("a risk register collection is already running")
)

// Options carries the deployment settings the collectors need.
type Options struct {
	ServerURL            string
	NetworkPolicyEnabled bool
	DefaultDenyAllEgress bool
	MCPRetentionDays     int
	LLMRetentionDays     int
	GitMaxRepoSizeMB     int
}

// Runner runs collection: it reads every configured source, runs the checks, and brings the
// register in line with what was found.
type Runner struct {
	gateway *gclient.Client
	storage kclient.Client
	opts    Options
	analyst *Analyst
	running atomic.Bool
}

// NewRunner returns a runner. engine may be nil, in which case runs skip the model-assisted
// steps.
func NewRunner(gateway *gclient.Client, storage kclient.Client, engine completionEngine, opts Options) *Runner {
	runner := &Runner{gateway: gateway, storage: storage, opts: opts}
	if engine != nil {
		runner.analyst = &Analyst{Storage: storage, Engine: engine}
	}
	return runner
}

// Start begins a collection run in the background and returns its record. The run outlives
// the request that started it.
func (r *Runner) Start(ctx context.Context, trigger string) (*gtypes.RiskCollectionRun, error) {
	if !r.running.CompareAndSwap(false, true) {
		return nil, ErrRunInProgress
	}
	run := &gtypes.RiskCollectionRun{StartedAt: time.Now().UTC(), Trigger: trigger}
	if err := r.gateway.CreateRiskRun(ctx, run); err != nil {
		r.running.Store(false)
		return nil, err
	}
	started := *run
	go func() {
		defer r.running.Store(false)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runTimeout)
		defer cancel()
		r.finish(ctx, run)
	}()
	return &started, nil
}

// Run does a collection run and waits for it.
func (r *Runner) Run(ctx context.Context, trigger string) (*gtypes.RiskCollectionRun, error) {
	if !r.running.CompareAndSwap(false, true) {
		return nil, ErrRunInProgress
	}
	defer r.running.Store(false)

	run := &gtypes.RiskCollectionRun{StartedAt: time.Now().UTC(), Trigger: trigger}
	if err := r.gateway.CreateRiskRun(ctx, run); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	r.finish(ctx, run)
	if run.Error != "" {
		return run, errors.New(run.Error)
	}
	return run, nil
}

// finish does the work of a run and saves its outcome, whatever that is.
func (r *Runner) finish(ctx context.Context, run *gtypes.RiskCollectionRun) {
	if err := r.collect(ctx, run); err != nil {
		run.Error = err.Error()
		slog.Error("Risk register collection failed", "error", err)
	}
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	// The outcome is saved even when the run's own context has expired.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := r.gateway.UpdateRiskRun(saveCtx, run); err != nil {
		slog.Error("Failed to save the risk register collection run", "error", err)
	}
	for _, status := range run.Sources {
		if err := r.gateway.SetRiskSourceStatus(saveCtx, status); err != nil {
			slog.Error("Failed to save a risk source's status", "source", status.Kind, "error", err)
		}
	}
}

func (r *Runner) collect(ctx context.Context, run *gtypes.RiskCollectionRun) error {
	collectors, skipped, err := r.collectors(ctx)
	if err != nil {
		return err
	}
	run.Sources = skipped

	snapshot := riskregister.Snapshot{CollectedAt: run.StartedAt, WindowDays: riskregister.DefaultWindowDays}
	var okSources []string
	for _, collector := range collectors {
		status := types.RiskSourceStatus{Kind: collector.Kind(), OK: true}
		counts, err := collector.Collect(ctx, &snapshot)
		status.Counts = counts
		if err != nil {
			status.OK, status.Error = false, err.Error()
			// Without Obot's own catalog, every server on a device would look unapproved.
			if collector.Kind() == types.RiskSourceObot {
				run.Sources = append(run.Sources, status)
				return fmt.Errorf("failed to read Obot's own configuration: %w", err)
			}
		} else {
			okSources = append(okSources, string(collector.Kind()))
		}
		run.Sources = append(run.Sources, status)
	}
	snapshot.Sources = run.Sources

	findings := riskregister.Detect(snapshot)
	var notes map[string]string
	if r.analyst != nil {
		// A failed model call costs the notes, not the run.
		if notes, err = r.analyst.Annotate(ctx, findings); err != nil {
			slog.Warn("Risk register findings were not annotated", "error", err)
		} else {
			run.Analyzed = len(findings) > 0
		}
	}

	risks := make([]gtypes.Risk, 0, len(findings))
	for _, finding := range findings {
		sources := make([]string, 0, len(finding.Sources))
		for _, source := range finding.Sources {
			sources = append(sources, string(source))
		}
		risks = append(risks, gtypes.Risk{
			Fingerprint: finding.Fingerprint(),
			Type:        finding.Type,
			Segment:     string(finding.Segment),
			Severity:    string(finding.Severity),
			Title:       finding.Title,
			Summary:     finding.Summary,
			Analysis:    notes[finding.Fingerprint()],
			Sources:     sources,
			Evidence:    finding.Evidence,
			Metrics:     finding.Metrics,
			Treatment:   finding.Treatment,
			Frameworks:  finding.Frameworks,
		})
	}
	result, err := r.gateway.SyncRisks(ctx, run.StartedAt, risks, okSources)
	if err != nil {
		return err
	}
	run.Found, run.New = result.Found, result.New

	counts, err := r.gateway.OpenRiskCountsBySegment(ctx)
	if err != nil {
		return err
	}
	open := make(map[types.RiskSegment]int, len(counts))
	for segment, count := range counts {
		open[types.RiskSegment(segment)] = count
	}
	profile := riskregister.BuildUsageProfile(snapshot, open)
	run.Profile = &profile
	return nil
}

// collectors builds a collector for every source that is turned on and configured, and a
// skipped status for the rest. Obot's own data and device scans need no configuration, so
// they are on unless turned off.
func (r *Runner) collectors(ctx context.Context) ([]Collector, []types.RiskSourceStatus, error) {
	stored, err := r.gateway.ListRiskSources(ctx)
	if err != nil {
		return nil, nil, err
	}
	sources := make(map[types.RiskSourceKind]gtypes.RiskSource, len(stored))
	for _, source := range stored {
		sources[types.RiskSourceKind(source.Kind)] = source
	}

	var (
		collectors []Collector
		skipped    []types.RiskSourceStatus
	)
	skip := func(kind types.RiskSourceKind, reason string) {
		skipped = append(skipped, types.RiskSourceStatus{Kind: kind, OK: true, Skipped: reason})
	}

	// Obot runs first: the other collectors' servers are compared against its catalog.
	collectors = append(collectors, &Obot{
		Gateway:              r.gateway,
		Storage:              r.storage,
		ServerURL:            r.opts.ServerURL,
		NetworkPolicyEnabled: r.opts.NetworkPolicyEnabled,
		DefaultDenyAllEgress: r.opts.DefaultDenyAllEgress,
		MCPRetentionDays:     r.opts.MCPRetentionDays,
		LLMRetentionDays:     r.opts.LLMRetentionDays,
	})
	collectors = append(collectors, &Sentry{Gateway: r.gateway})

	for _, kind := range []types.RiskSourceKind{types.RiskSourceOkta, types.RiskSourceClaudeCompliance, types.RiskSourceAnthropicAdmin, types.RiskSourceRepositories} {
		source, ok := sources[kind]
		if !ok {
			skip(kind, skippedNotConfigured)
			continue
		}
		if !source.Enabled {
			skip(kind, skippedDisabled)
			continue
		}
		secrets, err := r.gateway.RiskSourceSecrets(ctx, string(kind))
		if err != nil {
			return nil, nil, err
		}
		collector := r.outsideCollector(kind, source.Config, secrets)
		if collector == nil {
			skip(kind, skippedNotConfigured)
			continue
		}
		collectors = append(collectors, collector)
	}
	return collectors, skipped, nil
}

// outsideCollector builds the collector for a source outside Obot, or nil when the source
// is missing something it needs.
func (r *Runner) outsideCollector(kind types.RiskSourceKind, config, secrets map[string]string) Collector {
	switch kind {
	case types.RiskSourceOkta:
		if config[ConfigOktaOrgURL] == "" || secrets[SecretOktaAPIToken] == "" {
			return nil
		}
		okta := &Okta{OrgURL: config[ConfigOktaOrgURL], APIToken: secrets[SecretOktaAPIToken]}
		if r.analyst != nil {
			okta.Classify = r.analyst.ClassifyApps
		}
		return okta
	case types.RiskSourceClaudeCompliance:
		if secrets[SecretComplianceAPIKey] == "" {
			return nil
		}
		return &ClaudeCompliance{APIKey: secrets[SecretComplianceAPIKey]}
	case types.RiskSourceAnthropicAdmin:
		if secrets[SecretAnthropicAdminKey] == "" {
			return nil
		}
		return &AnthropicAdmin{AdminKey: secrets[SecretAnthropicAdminKey]}
	case types.RiskSourceRepositories:
		urls := SplitList(config[ConfigRepositoryURLs])
		if len(urls) == 0 {
			return nil
		}
		return &Repositories{
			URLs:            urls,
			GitCredentialID: config[ConfigGitCredentialID],
			MaxRepoSizeMB:   r.opts.GitMaxRepoSizeMB,
			Storage:         r.storage,
			Gateway:         r.gateway,
		}
	}
	return nil
}

// SplitList splits a newline- or comma-separated setting into its trimmed, non-empty parts.
func SplitList(value string) []string {
	var parts []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == ',' }) {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
