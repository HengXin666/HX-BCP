package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/routingrules"
)

// handleRoutingMCP is the MCP endpoint for the three routing operations the
// YAML issue requires: apply_rules, list_groups and get_rules. They call the
// same services as the HTTP API, including apply-and-rollback.
// See .agents/notes/implemented/feature/2026-10-18-yaml-routing-intent.md.
func (s *Server) handleRoutingMCP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		if s.writeMCPTooLarge(writer, request, err) {
			return
		}
		writeMCPError(writer, nil, -32700, "request body could not be read")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var call mcpRequest
	if err := decoder.Decode(&call); err != nil {
		writeMCPError(writer, nil, -32700, "request body is not JSON")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeMCPError(writer, call.ID, -32700, "request body must contain exactly one JSON value")
		return
	}
	if call.JSONRPC != "2.0" || call.Method == "" {
		writeMCPError(writer, call.ID, -32600, "invalid JSON-RPC request")
		return
	}
	if call.ID == nil {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	switch call.Method {
	case "initialize":
		writeMCPResult(writer, call.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "hx-pxy", "version": "1"},
		})
	case "tools/list":
		writeMCPResult(writer, call.ID, map[string]any{"tools": routingMCPTools()})
	case "tools/call":
		s.callRoutingTool(writer, request, call)
	default:
		writeMCPError(writer, call.ID, -32601, "method not found")
	}
}

func (s *Server) callRoutingTool(writer http.ResponseWriter, request *http.Request, call mcpRequest) {
	name, _ := call.Params["name"].(string)
	arguments, _ := call.Params["arguments"].(map[string]any)
	var text string
	var err error
	switch name {
	case "apply_rules":
		yamlText, _ := arguments["yaml"].(string)
		if s.routingRules == nil {
			err = errMCP("routing rules are not configured")
			break
		}
		config, applyErr := s.routingRules.ApplyYAML(request.Context(), yamlText)
		if applyErr != nil {
			err = applyErr
			break
		}
		encoded, _ := json.Marshal(config)
		text = string(encoded)
	case "get_rules":
		if s.routingRules == nil {
			err = errMCP("routing rules are not configured")
			break
		}
		config, getErr := s.routingRules.Get(request.Context())
		if getErr != nil {
			err = getErr
			break
		}
		encoded, _ := json.Marshal(config)
		text = string(encoded)
	case "list_groups":
		if s.proxyGroups == nil {
			err = errMCP("proxy groups are not configured")
			break
		}
		items, listErr := s.listProxyGroups(request.Context(), nil)
		if listErr != nil {
			err = listErr
			break
		}
		listed := make([]map[string]any, 0, len(items))
		for _, item := range items {
			listed = append(listed, map[string]any{"id": item.ID, "name": item.Name, "enabled": item.Enabled})
		}
		encoded, _ := json.Marshal(map[string]any{"items": listed})
		text = string(encoded)
	default:
		writeMCPError(writer, call.ID, -32602, "unknown tool")
		return
	}
	if err != nil {
		text := "operation failed"
		var safe mcpError
		if errors.As(err, &safe) || errors.Is(err, routingrules.ErrInvalid) {
			text = err.Error()
		} else {
			s.logger.ErrorContext(request.Context(), "MCP routing tool failed", "request_id", requestID(request), "error", err)
		}
		writeMCPResult(writer, call.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": true,
		})
		return
	}
	writeMCPResult(writer, call.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
	})
}

func routingMCPTools() []map[string]any {
	return []map[string]any{
		{"name": "apply_rules", "description": "Replace routing rules from a YAML intent document. Uses the same validation, hot reload and rollback as POST /api/v1/routing-rules/apply.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"yaml": map[string]any{"type": "string"}}, "required": []string{"yaml"}}},
		{"name": "list_groups", "description": "List proxy groups (id, name, enabled), the same set as GET /api/v1/proxy-groups.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
		{"name": "get_rules", "description": "Read the routing rules currently stored, including default and applies_to.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	}
}

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type mcpError string

func errMCP(text string) error { return mcpError(text) }

func (err mcpError) Error() string { return string(err) }

func (s *Server) writeMCPTooLarge(writer http.ResponseWriter, request *http.Request, err error) bool {
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		return false
	}
	s.writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "payload_too_large", "json body is larger than 64KiB")
	return true
}

func writeMCPResult(writer http.ResponseWriter, id any, result any) {
	writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeMCPError(writer http.ResponseWriter, id any, code int, message string) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message},
	})
}
