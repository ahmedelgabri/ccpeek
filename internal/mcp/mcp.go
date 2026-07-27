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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/jsonl"
	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

const protocolVersion = "2025-06-18"

// Status is the index-freshness snapshot behind the transport-owned
// `status` tool: whether a refresh pass is running and the v1 import
// outcome — MCP's equivalent of HTTP's /health. While Indexing is true
// tools read a visibly WARMING archive, not a frozen snapshot:
// per-source transactions commit as the pass runs and usage rollups
// regenerate at its end, so counts and totals can grow between calls.
type Status struct {
	Indexing      bool   `json:"indexing"`
	V1ImportState string `json:"v1ImportState,omitempty"`
	V1ImportError string `json:"v1ImportError,omitempty"`
}

// Server speaks MCP over one reader/writer pair.
type Server struct {
	svc     *query.Service
	version string
	status  func() Status
}

// New builds a Server. version is the ccpeek build version; status,
// when non-nil, backs the `status` tool so clients can tell a warming
// archive from a settled one.
func New(svc *query.Service, version string, status func() Status) *Server {
	return &Server{svc: svc, version: version, status: status}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcResponse always emits id. A nil json.RawMessage marshals to the
// literal null that JSON-RPC 2.0 requires when the request's id could not
// be determined, so the field must NOT carry omitempty — with it, a parse
// error's reply had no id member at all and strict clients reject that.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// parseError replies to a message whose id could not be read; its zero ID
// encodes as null.
func parseError(message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: message}}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxMessageBytes bounds one JSON-RPC message.
const maxMessageBytes = 10 * 1024 * 1024

// Serve processes requests until EOF or context cancellation.
//
// jsonl.Scan is the shared newline-delimited reader: an over-long message
// costs that MESSAGE, not the session. A bufio.Scanner capped at the same
// size returned ErrTooLong from Err() and Serve returned with it, so one
// oversized request killed the whole MCP connection.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	enc := json.NewEncoder(w)
	return jsonl.Scan(r, maxMessageBytes, func(_ int, line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return enc.Encode(parseError("parse error"))
		}
		if req.ID == nil {
			return nil // a notification (notifications/initialized) gets no reply
		}
		return enc.Encode(s.handle(ctx, req))
	}, func(_ int, size int64) error {
		return enc.Encode(parseError(fmt.Sprintf(
			"message of %d bytes exceeds the %d limit", size, maxMessageBytes,
		)))
	})
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
		defs := toolDefs
		if s.status != nil {
			defs = append(append([]map[string]any{}, defs...), statusToolDef)
		}
		resp.Result = map[string]any{"tools": defs}
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

// toolDefs derive from the operation registry, so the MCP surface can
// never drift from the CLI's — one definition serves both.
var toolDefs = buildToolDefs()

// statusToolDef is transport-owned, not a registry op: like HTTP's
// /health it describes THIS server process (index freshness), not the
// archive, so the CLI has no equivalent command to drift from.
var statusToolDef = map[string]any{
	"name": "status",
	"description": "Index freshness: whether a background refresh pass is running " +
		"(while true, reads see a warming archive — per-source commits land " +
		"incrementally and usage rollups regenerate at the end of the pass) " +
		"and the v1 import state.",
	"inputSchema": map[string]any{
		"type": "object", "properties": map[string]any{},
		"additionalProperties": false,
	},
}

func buildToolDefs() []map[string]any {
	var defs []map[string]any
	for _, op := range ops.Registry() {
		props := map[string]any{}
		var required []string
		for _, p := range op.Params {
			prop := map[string]any{"type": p.Type, "description": p.Desc}
			// Declared once in the registry, so every transport documents
			// the same default and the same ceiling — and both ride typed
			// JSON Schema keywords, so a client can check a call before
			// sending it rather than learning the bound from a 400.
			if p.Default != nil {
				prop["default"] = p.Default
			}
			if p.Max > 0 {
				prop["maximum"] = p.Max
			}
			props[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		// additionalProperties: false states on the wire what call()
		// enforces — a misspelled argument is an error, not a dropped
		// filter — so a client can catch it before sending.
		schema := map[string]any{
			"type": "object", "properties": props,
			"additionalProperties": false,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		defs = append(defs, map[string]any{
			"name":        op.Name,
			"description": op.Desc,
			"inputSchema": schema,
		})
	}
	return defs
}

// rejectUnknownArgs fails a call that carries an argument the tool does
// not declare. Dropping it silently answered a narrow question with the
// whole archive — `search` given `agent_slug` searched EVERY agent and
// presented the hits as filtered — and nothing in the reply said so. The
// message names the offenders and the tool's real arguments, so a model
// can correct the call and retry.
func rejectUnknownArgs(tool string, args map[string]json.RawMessage, params []ops.Param) error {
	valid := make(map[string]bool, len(params))
	names := make([]string, 0, len(params))
	for _, p := range params {
		valid[p.Name] = true
		names = append(names, p.Name)
	}
	var unknown []string
	for name := range args {
		if !valid[name] {
			unknown = append(unknown, strconv.Quote(name))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	noun := "argument"
	if len(unknown) > 1 {
		noun = "arguments"
	}
	expected := "it takes no arguments"
	if len(names) > 0 {
		expected = "valid arguments: " + strings.Join(names, ", ")
	}
	return fmt.Errorf("tool %q: unknown %s %s (%s)",
		tool, noun, strings.Join(unknown, ", "), expected)
}

func (s *Server) call(ctx context.Context, params json.RawMessage) (any, error) {
	// An absent params member decoded as "unexpected end of JSON input",
	// which tells a client nothing about what tools/call wants.
	if len(bytes.TrimSpace(params)) == 0 {
		return nil, fmt.Errorf(`tools/call needs a params object with a "name" ` +
			`(and "arguments" for tools that take any)`)
	}
	var call struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tool call params: %w", err)
	}
	if call.Name == "" {
		return nil, fmt.Errorf(`tool call params have no "name"`)
	}

	if call.Name == "status" && s.status != nil {
		if err := rejectUnknownArgs(call.Name, call.Arguments, nil); err != nil {
			return nil, err
		}
		text, err := json.MarshalIndent(ops.Wrap(s.status()), "", "  ")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
		}, nil
	}

	var op *ops.Op
	for _, o := range ops.Registry() {
		if o.Name == call.Name {
			op = &o
			break
		}
	}
	if op == nil {
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
	if err := rejectUnknownArgs(call.Name, call.Arguments, op.Params); err != nil {
		return nil, err
	}

	args := ops.Args{
		Str:  map[string]string{},
		Int:  map[string]int{},
		Bool: map[string]bool{},
	}
	for _, p := range op.Params {
		raw, present := call.Arguments[p.Name]
		if !present {
			if p.Required {
				return nil, fmt.Errorf("missing required argument %q", p.Name)
			}
			continue
		}
		switch p.Type {
		case "string":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("argument %q: want string", p.Name)
			}
			args.Str[p.Name] = v
		case "integer":
			var v int
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("argument %q: want integer", p.Name)
			}
			args.Int[p.Name] = v
		case "boolean":
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("argument %q: want boolean", p.Name)
			}
			args.Bool[p.Name] = v
		}
	}

	data, _, err := op.Run(ctx, s.svc, args)
	if err != nil {
		return nil, err
	}

	text, err := json.MarshalIndent(ops.Wrap(data), "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	}, nil
}
