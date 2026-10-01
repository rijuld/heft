package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/rijuld/heft/internal/weigh"
)

// heft mcp speaks the Model Context Protocol over stdio: newline-delimited
// JSON-RPC 2.0 on stdin/stdout, logs on stderr. It's small enough to write
// by hand, which keeps heft at one dependency.

const mcpLatest = "2025-11-25"

var mcpVersions = []string{mcpLatest, "2025-06-18", "2025-03-26", "2024-11-05"}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type toolArgs struct {
	Dir      string   `json:"dir"`
	Packages []string `json:"packages"`
	Tags     string   `json:"tags"`
	Module   string   `json:"module"`
	Use      []string `json:"use"`
	Base     string   `json:"base"`
}

var commonProps = map[string]any{
	"dir":      map[string]any{"type": "string", "description": "Directory of the Go module to analyze (default: the server's working directory)."},
	"packages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Package patterns naming the program's main packages (default: ./...)."},
	"tags":     map[string]any{"type": "string", "description": "Comma-separated build tags."},
}

func props(extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range commonProps {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

var moduleProp = map[string]any{"type": "string", "description": "Module path, or a unique suffix or substring of one (e.g. \"cobra\")."}

var mcpTools = []map[string]any{
	{
		"name":        "heft_weigh",
		"title":       "Weigh dependencies",
		"description": "Weigh every direct dependency of a Go program against what it actually uses: functions and lines reachable from main (RTA call graph), which modules and lines removing it would drop, and a verdict (inline, heavy, init-only, no-calls, shared, light, keep). Use before and after changing go.mod.",
		"inputSchema": map[string]any{"type": "object", "properties": props(nil)},
	},
	{
		"name":        "heft_why",
		"title":       "What you use from a module",
		"description": "Show exactly what a Go program uses from one module: the call sites into it, the reachable functions inside it, its license, and what removing it would drop.",
		"inputSchema": map[string]any{"type": "object", "properties": props(map[string]any{"module": moduleProp}), "required": []string{"module"}},
	},
	{
		"name":        "heft_try",
		"title":       "Weigh a module before adding it",
		"description": "Before running `go get`, weigh a candidate module as if the program called the given functions: how much of it you'd reach, which modules and lines it would bring (and which are new to this build), its license, and a verdict. Runs in a scratch module; never edits go.mod or go.sum.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"module": map[string]any{"type": "string", "description": "Module path, path@version, or a local directory starting with ./ or /."},
			"use":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What you'd call: Func, Type.Method, pkg.Func or pkg.Type.Method (pkg = last element of a package path in the module, or a full import path). Default: every exported function of the root package."},
			"dir":    commonProps["dir"],
			"tags":   commonProps["tags"],
		}, "required": []string{"module"}},
	},
	{
		"name":        "heft_extract",
		"title":       "Extract the code you use",
		"description": "Print the Go source of everything a program reaches in a module (functions plus the types, constants and variables they refer to), with the module's license on top: the starting point for replacing a small dependency with a copy.",
		"inputSchema": map[string]any{"type": "object", "properties": props(map[string]any{"module": moduleProp}), "required": []string{"module"}},
	},
	{
		"name":        "heft_diff",
		"title":       "Dependency changes since a commit",
		"description": "Compare the program's dependencies now with a git revision: modules added and removed, the change in third-party lines, new direct dependencies and changed verdicts. Use to review what a change added to the build.",
		"inputSchema": map[string]any{"type": "object", "properties": props(map[string]any{
			"base": map[string]any{"type": "string", "description": "Git revision to compare with, e.g. origin/main or HEAD~1."},
		}), "required": []string{"base"}},
	},
}

func serveMCP(in io.Reader, out, stderr io.Writer, c *config) int {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	enc := json.NewEncoder(out)
	reply := func(r rpcResponse) {
		r.JSONRPC = "2.0"
		if err := enc.Encode(r); err != nil {
			fmt.Fprintf(stderr, "heft mcp: %v\n", err)
		}
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			reply(rpcResponse{ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error: " + err.Error()}})
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification, e.g. notifications/initialized
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &p)
			v := mcpLatest
			if slices.Contains(mcpVersions, p.ProtocolVersion) {
				v = p.ProtocolVersion
			}
			reply(rpcResponse{ID: req.ID, Result: map[string]any{
				"protocolVersion": v,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "heft", "version": heftVersion()},
				"instructions":    "heft measures, with a whole-program call graph, how much of each Go dependency a program really uses and what it drags in. Call heft_try before adding a Go module, heft_diff or heft_weigh after changing go.mod, and heft_why / heft_extract when a dependency looks small enough to copy.",
			}})
		case "ping":
			reply(rpcResponse{ID: req.ID, Result: map[string]any{}})
		case "tools/list":
			reply(rpcResponse{ID: req.ID, Result: map[string]any{"tools": mcpTools}})
		case "tools/call":
			var p struct {
				Name      string   `json:"name"`
				Arguments toolArgs `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				reply(rpcResponse{ID: req.ID, Error: &rpcError{-32602, "invalid params: " + err.Error()}})
				continue
			}
			text, err := callTool(c, p.Name, p.Arguments)
			if errors.Is(err, errUnknownTool) {
				reply(rpcResponse{ID: req.ID, Error: &rpcError{-32602, err.Error()}})
				continue
			}
			isErr := err != nil
			if isErr {
				text = err.Error()
			}
			reply(rpcResponse{ID: req.ID, Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": isErr,
			}})
		default:
			reply(rpcResponse{ID: req.ID, Error: &rpcError{-32601, "method not found: " + req.Method}})
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(stderr, "heft mcp: %v\n", err)
		return 2
	}
	return 0
}

var errUnknownTool = errors.New("unknown tool")

// callTool runs one tool. The server's own loading flags (-toolchain,
// -offline, -cgo) apply; a tool call can't loosen them.
func callTool(c *config, name string, a toolArgs) (string, error) {
	opt := c.opts
	opt.Log = nil
	if a.Dir != "" {
		opt.Dir = a.Dir
	}
	opt.Patterns = a.Packages
	if a.Tags != "" {
		opt.Tags = a.Tags
	}
	asJSON := func(v any) (string, error) {
		b, err := json.MarshalIndent(v, "", "  ")
		return string(b), err
	}
	switch name {
	case "heft_weigh", "heft_why", "heft_extract":
		if name != "heft_weigh" && a.Module == "" {
			return "", errors.New("module is required")
		}
		rep, err := weigh.Analyze(opt)
		if err != nil {
			return "", err
		}
		if name == "heft_weigh" {
			return asJSON(rep)
		}
		m, err := rep.Lookup(a.Module)
		if err != nil {
			return "", err
		}
		if name == "heft_why" {
			return asJSON(whyJSON(rep, m))
		}
		return rep.Extract(m)
	case "heft_try":
		if a.Module == "" {
			return "", errors.New("module is required")
		}
		opt.Patterns = nil
		res, err := weigh.Try(a.Module, a.Use, opt)
		if err != nil {
			return "", err
		}
		return asJSON(res)
	case "heft_diff":
		if a.Base == "" {
			return "", errors.New("base is required")
		}
		base, err := weigh.AnalyzeAt(a.Base, opt)
		if err != nil {
			return "", err
		}
		head, err := weigh.Analyze(opt)
		if err != nil {
			return "", err
		}
		return asJSON(weigh.Diff(base, head, a.Base))
	}
	return "", fmt.Errorf("%w %q", errUnknownTool, name)
}
