package riskregister

import (
	"slices"
	"sort"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
)

var (
	segmentOrder = []types.RiskSegment{
		types.RiskSegmentCodingAgent,
		types.RiskSegmentBusinessUser,
		types.RiskSegmentAPIWorkload,
		types.RiskSegmentPlatform,
		types.RiskSegmentUnknown,
	}

	controlRank = map[types.RiskControl]int{
		types.RiskControlObserved: 0,
		types.RiskControlAudited:  1,
		types.RiskControlEnforced: 2,
	}
)

// BuildUsageProfile says who uses AI and how, by segment, from everything in a snapshot.
// The same tool seen by two sources is listed once, with the strongest control either
// source gives Obot over it. openRisks counts open register entries per segment.
func BuildUsageProfile(snapshot Snapshot, openRisks map[types.RiskSegment]int) types.RiskUsageProfile {
	tools := map[types.RiskSegment]map[string]*types.RiskUsageTool{}
	add := func(segment types.RiskSegment, tool types.RiskUsageTool) {
		if tools[segment] == nil {
			tools[segment] = map[string]*types.RiskUsageTool{}
		}
		key := strings.ToLower(tool.Name)
		existing, ok := tools[segment][key]
		if !ok {
			tools[segment][key] = &tool
			return
		}
		for _, source := range tool.Sources {
			if !slices.Contains(existing.Sources, source) {
				existing.Sources = append(existing.Sources, source)
			}
		}
		existing.Users = max(existing.Users, tool.Users)
		existing.Devices = max(existing.Devices, tool.Devices)
		existing.Keys = max(existing.Keys, tool.Keys)
		if controlRank[tool.Control] > controlRank[existing.Control] {
			existing.Control = tool.Control
		}
		if existing.Vendor == "" {
			existing.Vendor = tool.Vendor
		}
	}

	for _, observed := range snapshot.Clients {
		client := lookupClient(observed.Name)
		control := types.RiskControlObserved
		switch {
		case client.Enforced && snapshot.EnforcementEnabled:
			control = types.RiskControlEnforced
		case client.Audited:
			control = types.RiskControlAudited
		}
		add(client.Segment, types.RiskUsageTool{
			Name:    client.Display,
			Vendor:  client.Vendor,
			Sources: []types.RiskSourceKind{observed.Source},
			Users:   observed.Users,
			Devices: observed.Devices,
			Control: control,
		})
	}
	for _, app := range snapshot.Apps {
		if app.Match == nil || !app.Active {
			continue
		}
		add(app.Match.Segment, types.RiskUsageTool{
			Name:    app.Match.Name,
			Vendor:  app.Match.Vendor,
			Sources: []types.RiskSourceKind{app.Source},
			Users:   app.AssignedUsers,
			Control: types.RiskControlObserved,
		})
	}
	for _, activity := range snapshot.Activity {
		add(activity.Segment, types.RiskUsageTool{
			Name:    activity.Product,
			Sources: []types.RiskSourceKind{activity.Source},
			Users:   len(activity.Users),
			Control: types.RiskControlObserved,
		})
	}

	keys := map[string]*types.RiskUsageTool{}
	for _, key := range snapshot.APIKeys {
		if !key.Active {
			continue
		}
		if keys[key.Provider] == nil {
			keys[key.Provider] = &types.RiskUsageTool{
				Name:    key.Provider + " API keys",
				Vendor:  key.Provider,
				Sources: []types.RiskSourceKind{key.Source},
				Control: types.RiskControlObserved,
			}
		}
		keys[key.Provider].Keys++
	}
	for _, tool := range keys {
		add(types.RiskSegmentAPIWorkload, *tool)
	}

	if gateway := snapshot.Gateway; gateway != nil {
		// The gateways don't yet say which kind of client a request came from, so their
		// traffic can't be split by segment.
		if len(gateway.LLMUsers) > 0 {
			add(types.RiskSegmentUnknown, types.RiskUsageTool{
				Name:    "Obot LLM gateway",
				Sources: []types.RiskSourceKind{types.RiskSourceObot},
				Users:   len(gateway.LLMUsers),
				Control: types.RiskControlEnforced,
			})
		}
		if len(gateway.MCPUsers) > 0 {
			add(types.RiskSegmentUnknown, types.RiskUsageTool{
				Name:    "Obot MCP gateway",
				Sources: []types.RiskSourceKind{types.RiskSourceObot},
				Users:   len(gateway.MCPUsers),
				Control: types.RiskControlEnforced,
			})
		}
	}

	var profile types.RiskUsageProfile
	for _, segment := range segmentOrder {
		if len(tools[segment]) == 0 && openRisks[segment] == 0 {
			continue
		}
		summary := types.RiskUsageSegment{
			Segment:        segment,
			Tools:          []types.RiskUsageTool{},
			ToolsByControl: map[types.RiskControl]int{},
			OpenRisks:      openRisks[segment],
		}
		for _, tool := range tools[segment] {
			slices.Sort(tool.Sources)
			summary.Tools = append(summary.Tools, *tool)
			summary.Users = max(summary.Users, tool.Users)
			summary.Devices = max(summary.Devices, tool.Devices)
			summary.ToolsByControl[tool.Control]++
		}
		sort.Slice(summary.Tools, func(i, j int) bool {
			a, b := summary.Tools[i], summary.Tools[j]
			if a.Users != b.Users {
				return a.Users > b.Users
			}
			return a.Name < b.Name
		})
		profile.Segments = append(profile.Segments, summary)
	}
	return profile
}
