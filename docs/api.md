# wvf HTTP API

Served by `wvf serve` on `listen` (default `127.0.0.1:8420`). JSON in/out.

## Scan

### `POST /api/scan`

Trigger a scan for any URL — the site key is created on first sight.

```json
{"url": "http://localhost:3000", "wait": 90, "deep": false}
```

| field | meaning |
| --- | --- |
| `url` | required; normalized via sitekey (`localhost:3000` etc.) |
| `wait` | optional seconds, ≤300 — blocks until done/failed/timeout |
| `deep` | optional — add web-vitals attribution + lighthouse |
| `only` | optional comma list — `ttfb,fcp,lcp,cls,tbt,inp,vitals,http,h3,images,js,css,deep,psi` (combine several e.g. `lcp,cls`); overrides the site's stored only |

Sync response: `{"job": 3, "site": "localhost:3000", "state": "done",
"run_id": 12, "run": {...}}`. Async: omit `wait`, poll the job.

### `GET /api/jobs/{id}`

`{"job": {...state, detail...}, "run_id": 12}`

### `POST /api/scan-now`

`{key, deep, only, page}` — `page` scans a sub-path
(`site.URL + "/" + page`); report shows `page <path>`.

### `POST /api/scan-all`

`{deep}` — enqueues a scan for every monitored site (schedule set).
`deep=true` forces deep mode for all. Returns `{"enqueued": N}`.

### `GET /api/sites/{key}/pages`

Sitemap-derived path list for page scanning — parses the site's
`sitemap.xml` `<loc>`s into paths (falls back to `["/"]`). Any path may
be passed to `scan-now {page}` even if not in the sitemap.

### `GET /api/jobs/stream`

SSE stream — pushes the full jobs list whenever it changes (1s tick,
deltas only). Dashboard uses `EventSource`; never gzip/br-compressed.
REST `/api/jobs` remains for snapshot clients.

`{"key": "localhost:3000"}` → `{"job": N}` — uses the site's `deep` flag.

## Read

- `GET /api/sites` — every site + config
- `GET /api/sites/{key}/runs` — newest-first run history (headline metrics)
- `GET /api/runs/{id}` — one run incl. `detail_json` (samples, responses
  with per-request timing, http_checks + cache signals, h3,
  attribution, lighthouse, psi; samples[].console holds captured
  page console warn/error/exception entries for JS debugging)
- `GET /api/jobs` — recent jobs (progress via `detail`)

## IDE integration (VS Code etc.)

No plugin needed — two good options:

**tasks.json** (`.vscode/tasks.json`) — simplest:

```json
{"version": "2.0.0",
 "tasks": [{
   "label": "wvf: scan current site",
   "type": "shell",
   "command": "curl -s -X POST http://127.0.0.1:8420/api/scan -d '{\"url\":\"http://localhost:3000\",\"wait\":90}' | jq '{state, run_id, ttfb: .run.TTFBms, lcp: .run.LCPms, cls: .run.CLS}'",
   "problemMatcher": []}]}
```

Bind to a key or run via Terminal → Run Task. Extend with `--deep` or
`jq` slicing whatever fields matter.

**A dedicated extension** would be ~100 lines (axios POST + webview for
the trend) — worth it only if you want in-editor charts. The dashboard
at `http://127.0.0.1:8420` covers that today.

## MCP server (native agent tools)

Two transports, same tool surface:

### `POST /mcp` — HTTP transport

When `wvf serve` is running, the same MCP tools are exposed over the
Streamable-HTTP transport — point the client at the URL instead of
spawning a process:

```json
{"mcpServers": {"wvf": {"url": "http://127.0.0.1:8420/mcp"}}}
```

One JSON-RPC 2.0 request per POST (`initialize`, `tools/list`,
`tools/call`; `notifications/*` get `202`). Scans enqueue through the
daemon's job runner — single-slot, progress visible on the dashboard —
then the call waits (≤300s, same cap as `wait`) and returns the same
payload as stdio. On timeout/failure it reports
`{"site", "job", "state"}` so the agent can poll via the `jobs` tool.
Note: `samples` is honored on stdio only — the job queue carries
url/deep/only, so HTTP scans use the server's configured `samples`.

### `wvf mcp` — stdio transport

Prefer HTTP when the daemon is already up; otherwise spawn the binary
as a stdio MCP process:

```json
{"mcpServers": {"wvf": {
  "command": "/path/to/bin/wvf", "args": ["mcp"],
  "env": {"WVF_STATE_DB": "/path/to/state.db"}}}}
```

Tools: `scan {url, deep, only, samples}` (synchronous, returns metrics +
the unified **findings** list — our checks + `lh_*` + `attr_*` + `psi_*`
run_id), `sites`, `history {site, limit}`, `run {id}` (full detail incl.
per-request timing + findings), `jobs {limit}`, `prune`. Works in Devin,
Cursor, Claude Desktop — no per-IDE code needed.

**AI IDEs / agents** that don't speak MCP: give the agent this file —
the REST API is directly callable via its terminal/HTTP tools.

## Errors

`{"error": "..."}` + non-200 status. Timeouts on `wait` return
`{"state": "timeout"}` — the job keeps running server-side.
