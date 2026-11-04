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

Sync response: `{"job": 3, "site": "localhost:3000", "state": "done",
"run_id": 12, "run": {...}}`. Async: omit `wait`, poll the job.

### `GET /api/jobs/{id}`
`{"job": {...state, detail...}, "run_id": 12}`

### `POST /api/scan-now`
`{"key": "localhost:3000"}` → `{"job": N}` — uses the site's `deep` flag.

## Read

- `GET /api/sites` — every site + config
- `GET /api/sites/{key}/runs` — newest-first run history (headline metrics)
- `GET /api/runs/{id}` — one run incl. `detail_json` (samples, responses
  with per-request timing, http_checks, h3, attribution, lighthouse, psi)
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

**AI IDEs / agents** (Devin, Cursor, Cline): give the agent this file +
the API is directly callable via its terminal/HTTP tools — this is the
integration the API was designed for. An MCP wrapper (`wvf mcp`) would
be the native-agent option if direct tool use proves awkward.

## Errors

`{"error": "..."}` + non-200 status. Timeouts on `wait` return
`{"state": "timeout"}` — the job keeps running server-side.
