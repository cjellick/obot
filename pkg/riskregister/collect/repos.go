package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/git"
	"github.com/obot-platform/obot/pkg/gitcredential"
	"github.com/obot-platform/obot/pkg/riskregister"
	"github.com/obot-platform/obot/pkg/system"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// maxRepoFileBytes bounds the size of a configuration file that is parsed.
	maxRepoFileBytes = 1 << 20
	// maxRepositories bounds how many repositories one run clones.
	maxRepositories = 50

	claudeBypassMode = "bypassPermissions"
)

var (
	// mcpConfigFiles are the repository files that define MCP servers, with the JSON key
	// that holds them.
	mcpConfigFiles = map[string]string{
		".mcp.json":               "mcpServers",
		".cursor/mcp.json":        "mcpServers",
		".kiro/settings/mcp.json": "mcpServers",
		".vscode/mcp.json":        "servers",
	}

	// agentSettingsFiles are the repository files that set an agent's permissions.
	agentSettingsFiles = []string{".claude/settings.json", ".claude/settings.local.json"}

	// skippedDirs are never descended into.
	skippedDirs = []string{".git", "node_modules", "vendor", ".venv", "dist", "build"}
)

// Repositories clones each configured repository and reads the AI agent configuration
// checked into it: the MCP servers it defines and settings that widen what an agent may do.
type Repositories struct {
	URLs []string
	// GitCredentialID optionally names a stored Git credential to clone with.
	GitCredentialID string
	MaxRepoSizeMB   int
	Storage         kclient.Client
	Gateway         *gclient.Client
}

type repoMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

type claudeSettings struct {
	EnableAllProjectMCPServers bool `json:"enableAllProjectMcpServers"`
	Permissions                struct {
		DefaultMode string   `json:"defaultMode"`
		Allow       []string `json:"allow"`
	} `json:"permissions"`
}

func (*Repositories) Kind() types.RiskSourceKind {
	return types.RiskSourceRepositories
}

func (r *Repositories) Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error) {
	urls := r.URLs
	if len(urls) > maxRepositories {
		urls = urls[:maxRepositories]
	}

	var (
		scanned int
		before  = len(snapshot.MCPServers)
		errs    []error
	)
	for _, repoURL := range urls {
		if err := r.scan(ctx, repoURL, snapshot); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", repoURL, err))
			continue
		}
		scanned++
	}
	counts := map[string]int{"repositories": scanned, "mcpServers": len(snapshot.MCPServers) - before, "settings": len(snapshot.RepoSettings)}
	// A repository that can't be cloned doesn't hide what the others showed, but the
	// source is reported as failed so its earlier entries aren't cleared.
	return counts, errors.Join(errs...)
}

func (r *Repositories) scan(ctx context.Context, repoURL string, snapshot *riskregister.Snapshot) error {
	var token string
	if r.GitCredentialID != "" {
		var err error
		if token, err = gitcredential.Resolve(ctx, r.Storage, r.Gateway, system.DefaultNamespace, r.GitCredentialID, repoURL); err != nil {
			return err
		}
	}
	dir, _, cleanup, err := git.Clone(ctx, repoURL, token, "", r.MaxRepoSizeMB)
	if err != nil {
		return err
	}
	defer cleanup()

	name := repositoryName(repoURL)
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			for _, skipped := range skippedDirs {
				if entry.Name() == skipped {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// Only regular files are read, so a symlink can't point the scan outside the clone.
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		for suffix, key := range mcpConfigFiles {
			if rel == suffix || strings.HasSuffix(rel, "/"+suffix) {
				servers, err := readMCPConfig(path, key)
				if err != nil {
					// A malformed file is somebody's work in progress, not a reason to
					// stop reading the repository.
					return nil
				}
				snapshot.MCPServers = append(snapshot.MCPServers, repoServers(name, rel, servers)...)
			}
		}
		for _, suffix := range agentSettingsFiles {
			if rel == suffix || strings.HasSuffix(rel, "/"+suffix) {
				snapshot.RepoSettings = append(snapshot.RepoSettings, readClaudeSettings(path, name, rel)...)
			}
		}
		return nil
	})
}

func readMCPConfig(path, key string) (map[string]repoMCPServer, error) {
	data, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	servers := map[string]repoMCPServer{}
	if raw, ok := file[key]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, err
		}
	}
	return servers, nil
}

func repoServers(repository, file string, servers map[string]repoMCPServer) []riskregister.MCPServerObservation {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	observations := make([]riskregister.MCPServerObservation, 0, len(names))
	for _, name := range names {
		server := servers[name]
		transport := server.Type
		if transport == "" {
			transport = "stdio"
			if server.URL != "" {
				transport = "http"
			}
		}
		observations = append(observations, riskregister.MCPServerObservation{
			Source:    types.RiskSourceRepositories,
			Key:       repository + ":" + file + ":" + name,
			Name:      name,
			Transport: transport,
			URL:       server.URL,
			Command:   server.Command,
			Args:      server.Args,
			Location:  fmt.Sprintf("%s (%s)", repository, file),
		})
	}
	return observations
}

// readClaudeSettings reports the Claude Code project settings that let an agent act without
// asking: turned-off permission prompts, every project MCP server started automatically, and
// an allow rule that covers any shell command.
func readClaudeSettings(path, repository, file string) []riskregister.RepoSettingObservation {
	data, err := readBounded(path)
	if err != nil {
		return nil
	}
	var settings claudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}

	var found []riskregister.RepoSettingObservation
	add := func(setting, detail string) {
		found = append(found, riskregister.RepoSettingObservation{
			Source:     types.RiskSourceRepositories,
			Repository: repository,
			File:       file,
			Setting:    setting,
			Detail:     detail,
		})
	}
	if settings.Permissions.DefaultMode == claudeBypassMode {
		add(riskregister.RepoSettingBypassPermissions, "Claude Code's permission prompts are turned off for this project.")
	}
	if settings.EnableAllProjectMCPServers {
		add(riskregister.RepoSettingAllProjectMCP, "Every MCP server the repository defines is started without asking.")
	}
	for _, rule := range settings.Permissions.Allow {
		if rule == "Bash" || rule == "Bash(*)" || rule == "Bash(:*)" {
			add(riskregister.RepoSettingBroadAllow, fmt.Sprintf("The allow rule %q lets Claude Code run any shell command without asking.", rule))
			break
		}
	}
	return found
}

func readBounded(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxRepoFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxRepoFileBytes)
	}
	return os.ReadFile(path)
}

// repositoryName shortens a clone URL to owner/name for display.
func repositoryName(repoURL string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(repoURL, "/"), ".git")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return trimmed
}
