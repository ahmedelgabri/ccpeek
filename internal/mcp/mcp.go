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
	"errors"
	"fmt"
	"io"
	"strings"

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

// rpcResponse carries ID as a POINTER so a nil id encodes as the literal
// null JSON-RPC 2.0 requires when the request's id could not be
// determined. With omitempty the field vanished entirely, and strict
// clients reject a response with neither id nor error placement.
type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// respondTo builds a reply carrying the request's id.
func respondTo(id json.RawMessage) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: &id}
}

// nullIDResponse is the shape for errors raised before an id could be
// read (parse errors): "id": null, per the spec.
func nullIDResponse(code int, message string) rpcResponse {
	null := json.RawMessage("null")
	return rpcResponse{JSONRPC: "2.0", ID: &null, Error: &rpcError{Code: code, Message: message}}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxMessageBytes bounds one JSON-RPC message.
const maxMessageBytes = 10 * 1024 * 1024

// Serve processes requests until EOF or context cancellation.
//
// An over-long line costs that MESSAGE, not the session. A bufio.Scanner
// capped at the same size returned ErrTooLong from Err() and Serve
// returned with it, so one oversized request killed the whole MCP
// connection instead of failing the request that caused it.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 64*1024)
	enc := json.NewEncoder(w)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := readMessage(br)
		if err == errMessageTooLong {
			if encErr := enc.Encode(nullIDResponse(-32700,
				"message exceeds the 10 MiB limit")); encErr != nil {
				return encErr
			}
			continue
		}
		if err != nil && err != io.EOF {
			return err
		}
		line := strings.TrimSpace(string(raw))
		if line == "" {
			if err == io.EOF {
				return nil
			}
			continue
		}
		var req rpcRequest
		if jsonErr := json.Unmarshal([]byte(line), &req); jsonErr != nil {
			if encErr := enc.Encode(nullIDResponse(-32700, "parse error")); encErr != nil {
				return encErr
			}
			if err == io.EOF {
				return nil
			}
			continue
		}
		if req.ID != nil {
			// A notification (e.g. notifications/initialized) gets no reply.
			if encErr := enc.Encode(s.handle(ctx, req)); encErr != nil {
				return encErr
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

var errMessageTooLong = errors.New("message exceeds the size limit")

// readMessage returns one newline-delimited message. Past
// maxMessageBytes it drains the rest of the line and reports
// errMessageTooLong, so the connection survives.
func readMessage(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			if len(line) > maxMessageBytes {
				// Drain to the newline so the next read starts on a message.
				for {
					_, derr := r.ReadSlice('\n')
					if derr != bufio.ErrBufferFull {
						break
					}
				}
				return nil, errMessageTooLong
			}
			continue
		}
		if err == nil && len(line) > maxMessageBytes {
			return nil, errMessageTooLong
		}
		return line, err
	}
}

func (s *Server) handle(ctx context.Context, req rpcRequest) rpcResponse {
	resp := respondTo(req.ID)
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
	"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
}

func buildToolDefs() []map[string]any {
	var defs []map[string]any
	for _, op := range ops.Registry() {
		props := map[string]any{}
		var required []string
		for _, p := range op.Params {
			props[p.Name] = map[string]string{"type": p.Type, "description": p.Desc}
			if p.Required {
				required = append(required, p.Name)
			}
		}
		schema := map[string]any{"type": "object", "properties": props}
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

func (s *Server) call(ctx context.Context, params json.RawMessage) (any, error) {
	var call struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tool call params: %w", err)
	}

	if call.Name == "status" && s.status != nil {
		text, err := json.MarshalIndent(map[string]any{
			"schema": "ccpeek/v1", "data": s.status(),
		}, "", "  ")
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

	text, err := json.MarshalIndent(map[string]any{"schema": "ccpeek/v1", "data": data}, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	}, nil
}
