// Package mcp exposes the query service as an MCP (Model Context
// Protocol) server over stdio, so any agent — Claude Code, Pi, Cursor —
// can ask "have I solved this before?", pull compact transcripts, or
// check spend natively (docs/v2-plan.md §5.7).
//
// The transport is newline-delimited JSON-RPC 2.0 on stdin/stdout, per
// the MCP stdio spec. The implementation is deliberately dependency-free:
// initialize, ping, tools/list, tools/call.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/query"
)

const protocolVersion = "2025-06-18"

// Server speaks MCP over one reader/writer pair.
type Server struct {
	svc     *query.Service
	version string
}

// New builds a Server. version is the ccpeek build version.
func New(svc *query.Service, version string) *Server {
	return &Server{svc: svc, version: version}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

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

// Serve processes requests until EOF or context cancellation.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if req.ID == nil {
			continue // notification (e.g. notifications/initialized): no response
		}
		resp := s.handle(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "ccpeek",
				"version": s.version,
			},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolDefs}
	case "tools/call":
		result, err := s.call(ctx, req.Params)
		if err != nil {
			// Tool-level failures are results with isError, not RPC errors.
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}
			return resp
		}
		resp.Result = result
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

// toolDefs mirror the query ops 1:1 (same surface as the CLI and API).
var toolDefs = []map[string]any{
	{
		"name":        "sessions",
		"description": "List coding-agent sessions, newest first. Filter by agent slug (claude-code, pi, codex, opencode, cursor), project path, date range (YYYY-MM-DD), or title substring. Returns tokens and estimated cost per session.",
		"inputSchema": schema(map[string]string{
			"agent": "string", "project": "string", "since": "string",
			"until": "string", "query": "string", "limit": "integer",
		}),
	},
	{
		"name":        "session",
		"description": "Get one session with everything related to it: token/cost totals, models used, relations (forks, resumes, sidechains), and linked artifacts (todos, plans, tasks, facets).",
		"inputSchema": schemaRequired(map[string]string{
			"agent": "string", "id": "string",
		}, "agent", "id"),
	},
	{
		"name":        "transcript",
		"description": "Read a session's transcript in order. Bounded by default (token-budget friendly); use from_seq/limit to page. Text only unless full=true.",
		"inputSchema": schemaRequired(map[string]string{
			"agent": "string", "id": "string", "from_seq": "integer",
			"limit": "integer",
		}, "agent", "id"),
	},
	{
		"name":        "usage",
		"description": "Token and estimated-cost aggregates from all agents, grouped by day, model, project, or agent, with optional date range. Unpriced groups are flagged.",
		"inputSchema": schema(map[string]string{
			"group": "string", "agent": "string", "since": "string", "until": "string",
		}),
	},
	{
		"name":        "search",
		"description": "Full-text search across all indexed sessions and artifacts from every agent — 'have I solved this before?'. Every hit resolves to a session.",
		"inputSchema": schemaRequired(map[string]string{
			"query": "string", "agent": "string", "limit": "integer",
		}, "query"),
	},
}

func (s *Server) call(ctx context.Context, params json.RawMessage) (any, error) {
	var call struct {
		Name      string `json:"name"`
		Arguments struct {
			Agent   string `json:"agent"`
			ID      string `json:"id"`
			Project string `json:"project"`
			Model   string `json:"model"`
			Since   string `json:"since"`
			Until   string `json:"until"`
			Query   string `json:"query"`
			Group   string `json:"group"`
			FromSeq int    `json:"from_seq"`
			Limit   int    `json:"limit"`
			Full    bool   `json:"full"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tool call params: %w", err)
	}
	a := call.Arguments

	var data any
	var err error
	switch call.Name {
	case "sessions":
		data, err = s.svc.Sessions(ctx, query.SessionsFilter{
			Agent: a.Agent, Project: a.Project, Model: a.Model,
			Since: a.Since, Until: a.Until, Query: a.Query, Limit: a.Limit,
		})
	case "session":
		data, err = s.svc.Session(ctx, a.Agent, a.ID)
	case "transcript":
		data, err = s.svc.Transcript(ctx, a.Agent, a.ID, query.TranscriptOptions{
			FromSeq: a.FromSeq, Limit: a.Limit, Full: a.Full,
		})
	case "usage":
		data, err = s.svc.Usage(ctx, query.UsageFilter{
			GroupBy: a.Group, Agent: a.Agent, Since: a.Since, Until: a.Until,
		})
	case "search":
		data, err = s.svc.Search(ctx, a.Query, query.SearchFilter{
			Agent: a.Agent, Limit: a.Limit,
		})
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
	if err != nil {
		return nil, err
	}

	text, err := json.MarshalIndent(map[string]any{"schema": "ccpeek/v1", "data": data}, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	}, nil
}

func schema(props map[string]string) map[string]any {
	return schemaRequired(props)
}

func schemaRequired(props map[string]string, required ...string) map[string]any {
	p := make(map[string]any, len(props))
	for name, typ := range props {
		p[name] = map[string]string{"type": typ}
	}
	out := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}
