# website-vitals-flow

Contained web-performance monitor for localhost and deployed sites —
Core Web Vitals + HTTP behaviour (h2/h3, compression, caching) via a
real Chrome, plus optional deep layers (web-vitals.js attribution,
Lighthouse audits, PSI). Single Go binary, SQLite state.

## Quick start

    go build -o bin/wvf ./cmd/wvf
    ./bin/wvf scan http://localhost:3000        # one-shot, saved under site key
    ./bin/wvf scan <url> --deep                 # + attribution + lighthouse
    ./bin/wvf scan <url> --psi                  # + PageSpeed Insights (FQDN only)
    ./bin/wvf sites add http://localhost:3000 --schedule 30m
    ./bin/wvf serve                             # dashboard + scheduler (127.0.0.1:8420)
    ./bin/wvf doctor                            # engine availability

## Dashboard

![dashboard](docs/img/dashboard.png)

## API (IDE/agent-callable)

Full reference: [docs/api.md](docs/api.md) — includes VS Code tasks.json
setup and notes on a future extension/MCP wrapper.

    POST /api/scan      {"url": "...", "wait": 90, "deep": true}  # sync or async
    GET  /api/jobs/{id}                              # poll → run_id
    GET  /api/sites/{key}/runs                       # history
    POST /api/scan-now  {"key": "..."}               # by site key

## Commands

`scan <url>` [--samples N] [--settle D] [--timeout D] [--json] [--html f] [--deep] [--psi]
`sites add|list|remove` · `report <id>` · `prune [--site k]` · `serve` · `doctor`

## Config (wvf.yml / WVF_* env)

`samples` 3 · `settle` 1.5s · `scan_timeout` 90s · `keep_last` 100 ·
`max_age_days` 56 · `listen` 127.0.0.1:8420 · `state_db` state.db ·
`browser_path` auto-detect · `psi_key`/`WVF_PSI_KEY` for PSI (unauthenticated
quota is ~0 — a key is required in practice)

## Design

- Engine tag per run (cdp|lighthouse|psi) — trend charts stay single-source
- Retention pruned inside the run-insert transaction
- Single-slot scan queue — all triggers (cli/manual/scheduled) share one path
- Lighthouse/node optional: `doctor` reports, scans degrade to CDP-only
- Field data (PSI) is opt-in via --psi, public FQDNs only, stored in the
  same detail_json but clearly separate from lab metrics
- `sites add --deep` makes scheduled + scan-now runs deep per-site;
  jobs carry a deep flag so one-off deep scans don't change the site
