// Package mcpserver exposes wvf as MCP tools over stdio (newline-delimited
// JSON-RPC 2.0) so MCP-capable IDEs/agents can drive scans natively.
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var tools = []map[string]any{
	{"name": "scan", "description": "Scan a URL (core web vitals + http checks). Args: url, deep(optional), samples(optional).",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"url":     map[string]string{"type": "string", "description": "URL to scan (http://localhost:3000, https://example.com)"},
			"deep":    map[string]string{"type": "boolean", "description": "also run web-vitals attribution + lighthouse"},
			"samples": map[string]string{"type": "integer", "description": "loads per scan (default from config)"},
			"only":    map[string]string{"type": "string", "description": "comma list to focus: lcp,cls,tbt,ttfb,fcp,http,deep,images"},
		}, "required": []string{"url"}}},
	{"name": "sites", "description": "List monitored sites.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "history", "description": "Recent runs for a site key. Args: site, limit(optional).",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"site":  map[string]string{"type": "string"},
			"limit": map[string]string{"type": "integer"},
		}, "required": []string{"site"}}},
	{"name": "run", "description": "Full detail of one run by id, incl. per-request timing and findings.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]string{"type": "integer"},
		}, "required": []string{"id"}}},
	{"name": "prune", "description": "Apply retention rules to all sites now.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

// Serve runs the MCP loop over r/w until EOF or ctx end.
func Serve(ctx context.Context, st *store.Store, cfg config.Config, r io.Reader, w io.Writer) error {
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	var mu sync.Mutex
	progress := func(string) {}
	for in.Scan() {
		var req rpcReq
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue
		}
		var resp rpcResp
		resp.JSONRPC = "2.0"
		resp.ID = req.ID
		switch req.Method {
		case "initialize":
			resp.Result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "wvf", "version": "0.1.0"},
			}
		case "notifications/initialized", "notifications/cancelled":
			continue
		case "ping":
			resp.Result = map[string]any{}
		case "tools/list":
			resp.Result = map[string]any{"tools": tools}
		case "tools/call":
			resp.Result = callTool(ctx, st, cfg, req.Params, &mu, progress)
		default:
			resp.Error = &rpcErr{Code: -32601, Message: "unknown method " + req.Method}
		}
		if req.ID != nil {
			mu.Lock()
			out, _ := json.Marshal(resp)
			fmt.Fprintf(w, "%s\n", out)
			mu.Unlock()
		}
	}
	return in.Err()
}

func callTool(ctx context.Context, st *store.Store, cfg config.Config,
	params json.RawMessage, mu *sync.Mutex, progress func(string)) any {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errResult("bad params: " + err.Error())
	}
	var args struct {
		URL     string `json:"url"`
		Site    string `json:"site"`
		ID      int64  `json:"id"`
		Limit   int    `json:"limit"`
		Deep    bool   `json:"deep"`
		Samples int    `json:"samples"`
		Only    string `json:"only"`
	}
	json.Unmarshal(p.Arguments, &args)

	switch p.Name {
	case "scan":
		if args.URL == "" {
			return errResult("url required")
		}
		key, canon, err := sitekey.From(args.URL)
		if err != nil {
			return errResult(err.Error())
		}
		site, err := st.GetOrCreateSite(ctx, key, canon, cfg.KeepLast, cfg.MaxAgeDays)
		if err != nil {
			return errResult(err.Error())
		}
		mu.Lock() // single-slot: MCP scans serialize
		defer mu.Unlock()
		if args.Samples > 0 {
			cfg.Samples = args.Samples
		}
		if args.Only != "" {
			site.Only = args.Only // per-request override of the site default
		}
		res, runID, err := jobs.RunSiteScan(ctx, st, cfg, site, progress, args.Deep)
		if err != nil {
			return errResult(err.Error())
		}
		last := res.Samples[len(res.Samples)-1]
		return okResult(map[string]any{
			"site": site.Key, "run_id": runID,
			"ttfb_ms": last.TTFBms, "fcp_ms": last.FCPms, "lcp_ms": last.LCPms,
			"cls": last.CLS, "tbt_ms": last.TBTms,
			"requests": len(last.Responses), "attribution": res.Attribution,
			"lighthouse": res.Lighthouse,
		})
	case "sites":
		sites, err := st.ListSites(ctx)
		if err != nil {
			return errResult(err.Error())
		}
		return okResult(sites)
	case "history":
		site, err := st.SiteByKey(ctx, args.Site)
		if err != nil {
			return errResult("unknown site " + args.Site)
		}
		runs, err := st.RunsForSite(ctx, site.ID, args.Limit)
		if err != nil {
			return errResult(err.Error())
		}
		return okResult(runs)
	case "run":
		run, err := st.RunByID(ctx, args.ID)
		if err != nil {
			return errResult(err.Error())
		}
		return okResult(run)
	case "prune":
		sites, err := st.ListSites(ctx)
		if err != nil {
			return errResult(err.Error())
		}
		var dropped int64
		for _, s := range sites {
			n, _ := st.PruneSiteNow(ctx, s.ID)
			dropped += n
		}
		return okResult(map[string]int64{"dropped": dropped})
	}
	return errResult("unknown tool " + p.Name)
}

func okResult(v any) any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []map[string]any{
		{"type": "text", "text": string(b)}}}
}

func errResult(msg string) any {
	return map[string]any{"isError": true, "content": []map[string]any{
		{"type": "text", "text": msg}}}
}

var _ = time.Now
