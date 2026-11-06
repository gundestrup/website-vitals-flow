// Package mcpserver exposes wvf as MCP tools so MCP-capable IDEs/agents
// can drive scans natively. Two transports share one Dispatcher: Serve
// (newline-delimited JSON-RPC 2.0 over stdio) and the server's POST /mcp
// endpoint — the only difference is the ScanFunc plugged in.
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/jobs"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/sitekey"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

// version is the serverInfo string in the initialize handshake.
const version = "0.1.0"

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
			"only":    map[string]string{"type": "string", "description": "comma list to focus: ttfb,fcp,lcp,cls,tbt,inp,vitals,http,h3,images,js,css,deep,psi — combine several e.g. lcp,cls"},
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
	{"name": "jobs", "description": "Recent jobs (queued/running/done/failed) — check background scan state. Args: limit(optional).",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"limit": map[string]string{"type": "integer"},
		}}},
	{"name": "prune", "description": "Apply retention rules to all sites now.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

// ScanArgs are the parsed arguments of the "scan" tool.
type ScanArgs struct {
	URL     string `json:"url"`
	Deep    bool   `json:"deep"`
	Samples int    `json:"samples"`
	Only    string `json:"only"`
}

// ScanFunc executes the "scan" tool and returns the result payload the
// dispatcher wraps into MCP content blocks. Two transports, two impls:
// stdio uses DirectScan (in-process RunSiteScan); the HTTP /mcp endpoint
// supplies an enqueue-and-wait func over the serve Runner so scans share
// the single-slot queue and surface on the dashboard.
type ScanFunc func(ctx context.Context, a ScanArgs) (map[string]any, error)

// Dispatcher answers MCP JSON-RPC methods — shared by the stdio loop
// (Serve) and the server's POST /mcp handler, so no tool logic lives in
// either transport.
type Dispatcher struct {
	st   *store.Store
	scan ScanFunc
}

// New builds a dispatcher; scan handles the "scan" tool (see ScanFunc).
func New(st *store.Store, scan ScanFunc) *Dispatcher {
	return &Dispatcher{st: st, scan: scan}
}

// DirectScan returns the in-process ScanFunc used by `wvf mcp` stdio:
// RunSiteScan inline, serialized so MCP scans can't overlap.
func DirectScan(st *store.Store, cfg config.Config, progress func(string)) ScanFunc {
	var mu sync.Mutex // single-slot: MCP scans serialize
	return func(ctx context.Context, a ScanArgs) (map[string]any, error) {
		key, canon, err := sitekey.From(a.URL)
		if err != nil {
			return nil, err
		}
		site, err := st.GetOrCreateSite(ctx, key, canon, cfg.KeepLast, cfg.MaxAgeDays)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if a.Samples > 0 {
			cfg.Samples = a.Samples
		}
		if a.Only != "" {
			site.Only = a.Only // per-request override of the site default
		}
		res, runID, err := jobs.RunSiteScan(ctx, st, cfg, site, progress, a.Deep)
		if err != nil {
			return nil, err
		}
		return ScanPayload(site.Key, runID, res), nil
	}
}

// ScanPayload shapes the scan-tool result from a finished measurement —
// used by both the stdio DirectScan and the HTTP runner-backed scan.
func ScanPayload(siteKey string, runID int64, res *measure.Result) map[string]any {
	last := res.Samples[len(res.Samples)-1]
	return map[string]any{
		"site": siteKey, "run_id": runID,
		"ttfb_ms": last.TTFBms, "fcp_ms": last.FCPms, "lcp_ms": last.LCPms,
		"cls": last.CLS, "tbt_ms": last.TBTms,
		"requests": len(last.Responses), "attribution": res.Attribution,
		"lighthouse": res.Lighthouse, "findings": res.Findings,
	}
}

// Dispatch handles one raw JSON-RPC message and returns the marshaled
// response. ok=false when nothing should be sent back (notifications —
// stdio writes nothing, HTTP answers 202).
func (d *Dispatcher) Dispatch(ctx context.Context, raw []byte) (out []byte, ok bool) {
	var req rpcReq
	resp := rpcResp{JSONRPC: "2.0"}
	if err := json.Unmarshal(raw, &req); err != nil {
		resp.Error = &rpcErr{Code: -32700, Message: "parse error: " + err.Error()}
		b, _ := json.Marshal(resp)
		return b, true
	}
	resp.ID = req.ID
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "wvf", "version": version},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		resp.Result = d.callTool(ctx, req.Params)
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return nil, false
		}
		resp.Error = &rpcErr{Code: -32601, Message: "unknown method " + req.Method}
	}
	if req.ID == nil { // notification form — no response expected
		return nil, false
	}
	b, _ := json.Marshal(resp)
	return b, true
}

// Serve runs the MCP loop over r/w until EOF or ctx end — the stdio
// transport around the shared Dispatcher with the DirectScan impl.
func Serve(ctx context.Context, st *store.Store, cfg config.Config, r io.Reader, w io.Writer) error {
	d := New(st, DirectScan(st, cfg, func(string) {}))
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		if out, ok := d.Dispatch(ctx, in.Bytes()); ok {
			_, _ = fmt.Fprintf(w, "%s\n", out)
		}
	}
	return in.Err()
}

func (d *Dispatcher) callTool(ctx context.Context, params json.RawMessage) any {
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
	_ = json.Unmarshal(p.Arguments, &args)
	st := d.st

	switch p.Name {
	case "scan":
		if args.URL == "" {
			return errResult("url required")
		}
		payload, err := d.scan(ctx, ScanArgs{
			URL: args.URL, Deep: args.Deep,
			Samples: args.Samples, Only: args.Only,
		})
		if err != nil {
			return errResult(err.Error())
		}
		return okResult(payload)
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
		// surface the unified findings list (our checks + lh +
		// attribution + psi merged at persist) alongside the run
		var det struct {
			Findings json.RawMessage `json:"findings"`
		}
		_ = json.Unmarshal([]byte(run.DetailJSON), &det)
		return okResult(map[string]any{
			"run": run, "findings": det.Findings})
	case "jobs":
		limit := args.Limit
		if limit == 0 {
			limit = 20
		}
		jobs, err := st.RecentJobs(ctx, limit)
		if err != nil {
			return errResult(err.Error())
		}
		return okResult(jobs)
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
