// Command podium-mcp is the MCP server bridge described in spec §6. It
// exposes the meta-tools (load_domain, search_domains, search_artifacts,
// load_artifact) over MCP's stdio transport, forwards calls to a Podium
// registry server, and runs the configured HarnessAdapter at materialize
// time.
//
// Stage 3 ships a stdio I/O loop with a JSON-RPC 2.0 envelope that
// matches the MCP wire protocol's request / response shape (initialize,
// tools/list, tools/call). Identity, caching, and the materialization
// pipeline land alongside their respective phases.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

const protocolVersion = "2024-11-05"

func main() {
	registry := os.Getenv("PODIUM_REGISTRY")
	if registry == "" {
		fmt.Fprintln(os.Stderr, "error: PODIUM_REGISTRY is required")
		os.Exit(2)
	}
	srv := &mcpServer{registry: registry, http: &http.Client{}}
	if err := srv.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type mcpServer struct {
	registry string
	http     *http.Client
}

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the matching response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *mcpServer) serve(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		resp := s.handle(req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *mcpServer) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":               map[string]any{},
				"prompts":             map[string]any{},
				"sessionCorrelation":  true,
			},
			"serverInfo": map[string]any{"name": "podium-mcp", "version": "0.0.0-dev"},
		}
	case "tools/list":
		resp.Result = map[string]any{
			"tools": []map[string]any{
				{"name": "load_domain", "description": "Browse the artifact catalog hierarchically."},
				{"name": "search_domains", "description": "Search the catalog for relevant domains."},
				{"name": "search_artifacts", "description": "Search or browse the artifact catalog."},
				{"name": "load_artifact", "description": "Load a specific artifact by ID."},
			},
		}
	case "tools/call":
		resp.Result = s.callTool(req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *mcpServer) callTool(raw json.RawMessage) any {
	var p toolCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return map[string]any{"error": err.Error()}
	}
	switch p.Name {
	case "load_domain":
		return s.proxyGet("/v1/load_domain", p.Arguments)
	case "search_domains":
		return s.proxyGet("/v1/search_domains", p.Arguments)
	case "search_artifacts":
		return s.proxyGet("/v1/search_artifacts", p.Arguments)
	case "load_artifact":
		return s.proxyGet("/v1/load_artifact", p.Arguments)
	default:
		return map[string]any{"error": "unknown tool: " + p.Name}
	}
}

// proxyGet forwards the tool call to the registry HTTP API and returns the
// decoded JSON body as a map.
func (s *mcpServer) proxyGet(path string, args map[string]any) any {
	u, err := url.Parse(s.registry + path)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	q := u.Query()
	for k, v := range args {
		q.Set(k, fmt.Sprintf("%v", v))
	}
	u.RawQuery = q.Encode()
	resp, err := s.http.Get(u.String())
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return out
}
