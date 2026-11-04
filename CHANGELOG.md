# Changelog

All notable changes to website-vitals-flow. Format loosely follows
[Keep a Changelog](https://keepachangelog.com).

## Unreleased

### Added
- PSI layer (`internal/psi`): `--psi` flag, field-data p75 + lab metrics,
  `psi_key`/`WVF_PSI_KEY` config; fails clean when quota-limited
- `sites add --deep`: site-level deep flag (schema v4) — scheduler and
  scan-now honor it; `{"deep": true}` on `/api/scan` is a per-request
  override (jobs.deep)
- Lighthouse verified end-to-end: score + findings into detail_json

### Fixed
- web-vitals v5 attribution field names: `lcpEntry.element`,
  `resourceLoadDuration`, `largestShiftSource.node` — `lcp_element`
  now reports correctly (e.g. "H1")
- Attribution collection ran on the browser context (about:blank) —
  now opens its own navigated tab; added synthetic `visibilitychange`
  flush and poll-until-populated

## 0.1.0 — 2026-11-04 (initial working system)

### Phase 0-2: scaffold + CDP metrics + HTTP checks
- Go/Cobra CLI, chromedp v0.22 (generics API, remote.WebSocket)
- Buffered-observer injection for LCP/CLS/longtask (observer-only
  entry types); nav-timing TTFB; per-response protocol/encoding/
  status/encoded-bytes + full headers; DOM/JS-heap counters
- Encoding negotiation matrix (br/gzip/zstd), TLS version, alt-svc,
  quic-go direct h3 probe (advertised vs served), pquerna/cachecontrol
  RFC 7234 verdicts per asset; per-sample cache disabled
- Verified vs local sitespeed.io run on www.rutenskiklub.dk:
  FCP 300 vs 288 ms, LCP 300 vs 324 ms

### Phase 3: storage
- modernc.org/sqlite, WAL + busy_timeout, single writer, schema v1 +
  user_version migration harness (refuses newer schemas)
- sitekey normalization (localhost:3000 variants collapse)
- Retention (keep_last + max_age_days) pruned inside insert tx
- Commands: sites add|list|remove, report, prune

### Phase 4: app loop
- Single-slot jobs runner; RunSiteScan shared by cli/manual/scheduled
- Interval scheduler (30m/6h/daily); FailStaleJobs on boot
- stdlib serve: JSON API + embedded static UI, CSP headers
- IDE-callable API: POST /api/scan {url,wait}, GET /api/jobs/{id},
  jobs.run_id links (schema v2)

### Phase 5: UI
- Vendored bulma + alpine-csp + chart.js (no CDN, CSP-safe)
- Site list + last-run metrics + scan-now; per-engine trend charts
- `scan --html` self-contained report (__REPORT_DATA__ injection)

### Phase 6: deep engines
- internal/deep: vendored web-vitals.attribution bundle + collector
  (LCP element/sub-parts, CLS items, lab-INP); lighthouse CLI → JSON
  findings; `wvf doctor` engine capability report

### Phase 7: hardening
- Tests: retention in tx, idempotent site create, jobs lifecycle +
  FailStaleJobs, schema rejection, interval parsing, sitekey
- README.md

## Design notes
- Engine tag per run (cdp|lighthouse|psi); charts stay single-source
- PSI field data opt-in, FQDN only, never mixed with lab series
