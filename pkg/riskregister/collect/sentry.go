package collect

import (
	"context"
	"sort"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/riskregister"
)

const (
	// scanPageSize is how many devices' latest scans are read at a time.
	scanPageSize = 200
	// maxReplayServers bounds how many servers get a count of reported tool calls.
	maxReplayServers = 25
	// maxReplayCalls bounds how many reported calls are read per server.
	maxReplayCalls = 1000
)

// Sentry reads the latest scan of every device and the device allow rules.
type Sentry struct {
	Gateway *gclient.Client
}

type deviceTally struct {
	devices map[string]bool
	users   map[string]bool
}

func (*Sentry) Kind() types.RiskSourceKind {
	return types.RiskSourceSentry
}

func (s *Sentry) Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error) {
	configurations, err := s.Gateway.ListMDMConfigurations(ctx)
	if err != nil {
		return nil, err
	}
	for _, configuration := range configurations {
		if !configuration.EnforcementEnabled {
			continue
		}
		snapshot.EnforcementEnabled = true
		for _, server := range configuration.EnforcementAllowlist.Servers {
			switch {
			case server.URL != "":
				snapshot.Approved.URLs = append(snapshot.Approved.URLs, server.URL)
			case server.Hostname != "":
				snapshot.Approved.Hostnames = append(snapshot.Approved.Hostnames, server.Hostname)
			case server.Package != nil:
				snapshot.Approved.Packages = append(snapshot.Approved.Packages, riskregister.ServerPackage{Source: server.Package.Source, Name: server.Package.Name})
			}
		}
	}

	var (
		devices = map[string]bool{}
		clients = map[string]*deviceTally{}
		servers = map[string]*riskregister.MCPServerObservation{}
		tallies = map[string]*deviceTally{}
	)
	for offset := 0; ; offset += scanPageSize {
		scans, total, err := s.Gateway.ListDeviceScans(ctx, gclient.DeviceScanListOptions{GroupByDevice: true, Limit: scanPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, scan := range scans {
			device := scan.DeviceID
			if device == "" {
				device = scan.Hostname
			}
			devices[device] = true
			for _, client := range scan.Clients {
				tally(clients, strings.ToLower(client.Name), device, scan.Username)
			}
			for _, server := range scan.MCPServers {
				key := server.ConfigHash
				if key == "" {
					key = strings.Join(append([]string{server.Name, server.Transport, server.URL, server.Command}, server.Args...), "\x00")
				}
				if servers[key] == nil {
					servers[key] = &riskregister.MCPServerObservation{
						Source:    types.RiskSourceSentry,
						Key:       key,
						Name:      server.Name,
						Transport: server.Transport,
						URL:       server.URL,
						Command:   server.Command,
						Args:      server.Args,
					}
				}
				tally(tallies, key, device, scan.Username)
			}
		}
		if len(scans) == 0 || offset+len(scans) >= int(total) {
			break
		}
	}

	snapshot.Devices = len(devices)
	for name, t := range clients {
		snapshot.Clients = append(snapshot.Clients, riskregister.ClientObservation{Source: types.RiskSourceSentry, Name: name, Devices: len(t.devices), Users: len(t.users)})
	}
	sort.Slice(snapshot.Clients, func(i, j int) bool { return snapshot.Clients[i].Name < snapshot.Clients[j].Name })

	observed := make([]*riskregister.MCPServerObservation, 0, len(servers))
	for key, server := range servers {
		server.Devices, server.Users = len(tallies[key].devices), len(tallies[key].users)
		observed = append(observed, server)
	}
	sort.Slice(observed, func(i, j int) bool {
		if observed[i].Devices != observed[j].Devices {
			return observed[i].Devices > observed[j].Devices
		}
		return observed[i].Key < observed[j].Key
	})

	// Count the tool calls devices reported to the most widespread servers, so a proposed
	// allow rule can say what enforcement would have blocked.
	for i, server := range observed {
		if i >= maxReplayServers {
			break
		}
		calls, _, err := s.Gateway.GetMCPAuditLogs(ctx, gclient.MCPAuditLogOptions{
			SourceTypes: []types.AuditLogSourceType{types.AuditLogSourceTypeLocalAgentToolCall},
			MCPServer:   []string{server.Name},
			StartTime:   windowStart(snapshot),
			Limit:       maxReplayCalls,
		})
		if err != nil {
			return nil, err
		}
		users := map[string]bool{}
		for _, call := range calls {
			if call.LocalAgentToolCallFields != nil && call.LocalAgentToolCallFields.DeviceID != "" {
				users[call.LocalAgentToolCallFields.DeviceID] = true
			} else if call.UserID != "" {
				users[call.UserID] = true
			}
		}
		server.Calls, server.CallUsers = len(calls), len(users)
	}
	for _, server := range observed {
		snapshot.MCPServers = append(snapshot.MCPServers, *server)
	}

	return map[string]int{"devices": len(devices), "clients": len(clients), "mcpServers": len(servers)}, nil
}

func tally(tallies map[string]*deviceTally, key, device, user string) {
	if tallies[key] == nil {
		tallies[key] = &deviceTally{devices: map[string]bool{}, users: map[string]bool{}}
	}
	tallies[key].devices[device] = true
	if user != "" {
		tallies[key].users[strings.ToLower(user)] = true
	}
}
