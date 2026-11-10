package builtinmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/api/server/requestinfo"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
)

// auditBodyLimit bounds how much of a tool call's arguments and result the audit entry keeps.
const auditBodyLimit = 64 << 10

// auditToolCalls records each tool call on a built-in server in the MCP audit log, as the MCP
// gateway does for proxied servers, so calls from Obot AI and from agents connected to
// /mcp-connect/obot or /mcp-connect/obot-admin show up with the rest of the MCP traffic.
func auditToolCalls(req api.Context, mcpID, displayName, clientName string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, r mcp.Request) (mcp.Result, error) {
			if method != "tools/call" || req.GatewayClient == nil {
				return next(ctx, method, r)
			}
			start := time.Now()
			result, err := next(ctx, method, r)

			fields := &gtypes.MCPAuditLogFields{
				MCPID:                mcpID,
				MCPServerDisplayName: displayName,
				ClientName:           clientName,
				CallType:             method,
				ResponseReceived:     true,
				ResponseStatus:       http.StatusOK,
				ProcessingTimeMs:     time.Since(start).Milliseconds(),
			}
			if params, ok := r.GetParams().(*mcp.CallToolParamsRaw); ok && params != nil {
				fields.CallIdentifier = params.Name
				fields.RequestBody = auditJSON(map[string]any{"name": params.Name, "arguments": params.Arguments})
			}
			if session, ok := r.GetSession().(*mcp.ServerSession); ok && session != nil {
				if init := session.InitializeParams(); init != nil && init.ClientInfo != nil && clientName == "" {
					fields.ClientName, fields.ClientVersion = init.ClientInfo.Name, init.ClientInfo.Version
				}
			}
			switch {
			case err != nil:
				fields.ResponseStatus = http.StatusInternalServerError
				fields.Error = err.Error()
			case result != nil:
				fields.ResponseBody = auditJSON(result)
				if tr, ok := result.(*mcp.CallToolResult); ok && tr.IsError {
					fields.ResponseStatus = http.StatusBadRequest
					for _, c := range tr.Content {
						if t, ok := c.(*mcp.TextContent); ok {
							fields.Error = truncate(strings.TrimSpace(t.Text), 1000)
							break
						}
					}
				}
			}
			req.GatewayClient.LogMCPAuditEntry(gtypes.MCPAuditLog{
				CreatedAt:  start.UTC(),
				SourceType: types.AuditLogSourceTypeMCP,
				UserID:     req.User.GetUID(),
				ClientIP:   requestinfo.GetSourceIP(req.Request),
				MCPFields:  fields,
			})
			return result, err
		}
	}
}

func auditJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if len(b) > auditBodyLimit {
		b, _ = json.Marshal(map[string]any{"truncated": true, "preview": string(b[:auditBodyLimit])})
	}
	return b
}
