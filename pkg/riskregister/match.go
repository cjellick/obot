package riskregister

import (
	"net/url"
	"path"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/enforcement"
)

// ApprovedServers is what Obot already treats as approved: its own hosted servers, catalog
// entries and Sentry allow rules.
type ApprovedServers struct {
	// ObotURL is Obot's own address. Servers reached through it are Obot-hosted.
	ObotURL   string
	URLs      []string
	Hostnames []string
	Packages  []ServerPackage
}

// ServerPackage is a registry package an MCP server runs from.
type ServerPackage struct {
	Source types.AllowlistServerPackageSource
	Name   string
}

// Match reports whether an observed server is approved, and why.
func (a ApprovedServers) Match(server MCPServerObservation) (bool, string) {
	if server.URL != "" {
		if a.ObotURL != "" && urlCovers(a.ObotURL, server.URL) {
			return true, "hosted by Obot"
		}
		for _, approved := range a.URLs {
			if urlCovers(approved, server.URL) {
				return true, "matches an approved URL"
			}
		}
		if host := hostOf(server.URL); host != "" {
			for _, approved := range a.Hostnames {
				if strings.EqualFold(approved, host) {
					return true, "matches an approved hostname"
				}
			}
		}
	}
	if pkg, ok := PackageOf(server.Command, server.Args); ok {
		for _, approved := range a.Packages {
			if approved.Source == pkg.Source && enforcement.CanonicalPackageName(approved.Source, approved.Name) == pkg.Name {
				return true, "matches an approved package"
			}
		}
	}
	return false, ""
}

// PackageOf works out the registry package a stdio server runs from, when its command is a
// package runner such as npx or uvx. The name is returned in its registry's canonical form.
func PackageOf(command string, args []string) (ServerPackage, bool) {
	runner := strings.ToLower(path.Base(strings.ReplaceAll(command, `\`, "/")))
	runner = strings.TrimSuffix(strings.TrimSuffix(runner, ".exe"), ".cmd")

	var source types.AllowlistServerPackageSource
	switch runner {
	case "npx", "bunx", "pnpx":
		source = types.AllowlistServerPackageSourceNPM
	case "pnpm", "yarn", "bun":
		if len(args) == 0 || (args[0] != "dlx" && args[0] != "x") {
			return ServerPackage{}, false
		}
		source, args = types.AllowlistServerPackageSourceNPM, args[1:]
	case "uvx":
		source = types.AllowlistServerPackageSourcePyPI
	case "uv":
		if len(args) < 2 || args[0] != "tool" || args[1] != "run" {
			return ServerPackage{}, false
		}
		source, args = types.AllowlistServerPackageSourcePyPI, args[2:]
	default:
		return ServerPackage{}, false
	}

	spec := packageSpec(args)
	if spec == "" {
		return ServerPackage{}, false
	}
	name := stripVersion(source, spec)
	if name == "" {
		return ServerPackage{}, false
	}
	return ServerPackage{Source: source, Name: enforcement.CanonicalPackageName(source, name)}, true
}

// packageSpec picks the package argument out of a runner's arguments: the value of
// --package/-p/--from when present, otherwise the first argument that isn't a flag.
func packageSpec(args []string) string {
	for i, arg := range args {
		switch {
		case arg == "--package" || arg == "-p" || arg == "--from":
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		case strings.HasPrefix(arg, "--package=") || strings.HasPrefix(arg, "--from="):
			return arg[strings.Index(arg, "=")+1:]
		}
	}
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// stripVersion removes a version or extras suffix from a package argument.
func stripVersion(source types.AllowlistServerPackageSource, spec string) string {
	if source == types.AllowlistServerPackageSourceNPM {
		// The first "@" of a scoped name is part of the name.
		if at := strings.LastIndex(spec, "@"); at > 0 {
			return spec[:at]
		}
		return spec
	}
	for _, sep := range []string{"[", "==", ">=", "<=", "~=", "@"} {
		if i := strings.Index(spec, sep); i > 0 {
			spec = spec[:i]
		}
	}
	return spec
}

// urlCovers reports whether candidate is at or under approved: the same scheme, host and
// port, with approved's path as a prefix on a segment boundary.
func urlCovers(approved, candidate string) bool {
	a, err := url.Parse(strings.TrimSpace(approved))
	if err != nil || a.Host == "" {
		return false
	}
	c, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil || c.Host == "" {
		return false
	}
	if !strings.EqualFold(a.Scheme, c.Scheme) || !strings.EqualFold(a.Hostname(), c.Hostname()) || portOf(a) != portOf(c) {
		return false
	}
	prefix := strings.TrimSuffix(a.Path, "/")
	if prefix == "" {
		return true
	}
	return c.Path == prefix || strings.HasPrefix(c.Path, prefix+"/")
}

func portOf(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	return "443"
}

func hostOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
