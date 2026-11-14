# Changelog

All notable changes to website-vitals-flow. Format loosely follows
[Keep a Changelog](https://keepachangelog.com).

## Unreleased

## 0.2.0 — 2026-11-14

### Added

- **MCP over HTTP**: `POST /mcp` Streamable-HTTP transport sharing the
  stdio Dispatcher; scans route through the serve Runner (single-slot);
  `{"url": "…/mcp"}` client config
- **Page scanning**: `jobs.page` (schema v7), `scan-now {page}`,
  `GET /api/sites/{key}/pages` (sitemap-derived paths + free input),
  `page <path>` tag on reports; dashboard Pages section
- **SSE live jobs**: `GET /api/jobs/stream` — job list pushed on change;
  dashboard EventSource replaces 3s polling
- **Compression serving**: gzip + brotli (`andybalholm/brotli`) response
  middleware — dogfooded from our own `brotli not negotiated` finding
- **Theme + i18n**: auto/light/dark toggle (localStorage + prefers-color-
  scheme) and en/da UI dictionary
- **SwiftBar plugin** (`scripts/swiftbar/`): menubar status/jobs/scan-all/
  start-stop + self-verifying install/uninstall + lifecycle tests
- **Serve self-replace**: `wvf serve` terminates a lingering `wvf serve`
  on the same port (non-wvf listeners untouched)
- **Version stamping**: `git describe` via ldflags → `wvf version`
- Module renamed `forgejo…` → `github.com/gundestrup` (real remote);
  report shell via shared `layout.tmpl` (html/template)
- Console capture, shared `internal/pipeline`, scan-all endpoint/buttons,
  full lint stack (biome/html-validate/markdownlint/yaml/uiqa/smoke),
  quality gates, docs screenshots — see commits 0db463b…38fa645

### Fixed

- web-vitals v5 attribution field names: `lcpEntry.element`,
  `resourceLoadDuration`, `largestShiftSource.node` — `lcp_element`
  now reports correctly (e.g. "H1")
- Attribution collection ran on the browser context (about:blank) —
  now opens its own navigated tab; added synthetic `visibilitychange`
  flush and poll-until-populated

- Site keys containing `/` (self-scan detail URLs) broke
  `/api/sites/{key}/runs` → `load()` aborted → cards vanished —
  `encodeURIComponent` on key interpolation
- `loadPages`/`scanPage` call sites without definitions → `open()` catch
  nulled `det` → shot card + findings hidden
- Sitemap `<loc>` absolute URLs parsed via `url.Parse(loc).Path`
- Console card `id` fetched nonexistent `/badges/console-*.svg` — `id:''`
- gzip middleware buffered SSE + dropped `http.Flusher` — skipped on
  `Accept: text/event-stream`, `Flush()` passthrough
- Selects wrapped per Bulma `<div class="select">` pattern; `job-meta`
  span→div (block-in-inline); report inline styles → utility classes
- Stored XSS in report (findings/console text now `html.EscapeString`'d);
  semgrep/gosec annotations on justified sites
- `keepLast=0` prunes immediately — surfaced by new report test

### Security

- Dev-tool dep upgrades clearing all npm audit + Dependabot +
  Semgrep supply-chain findings: `markdownlint-cli2` ^0.24.0
  (js-yaml 4.1.0→5.x, markdown-it 14.1.0→15.x, markdownlint 0.42),
  `yaml` ^2.9.1, `katex` ≥0.18.2 via `overrides`
- h3 probe `tls.Config` pins `MinVersion: TLS 1.3` (QUIC floor anyway)
- CI actions pinned to release SHAs (checkout v7.0.1, setup-go v7.0.0)
- `#nosec`/`nosemgrep` annotations on remaining justified audit sites;
  `mdcheck` uses the linter's exit code (new summary format)
- `buildCards` split into per-card helpers — SonarLint complexity 47→<15

### Tests

- `TestPageScan` (scan-now page + sitemap endpoint), `TestSSEAndGzip`,
  `TestReportPageTag`, `TestMCPOverHTTP`, `TestJobPageRoundTrip`,
  score badge/metric, config load — all `-race` green

### Added (earlier unreleased)

- PSI layer (`internal/psi`): `--psi` flag, field-data p75 + lab metrics,
  `psi_key`/`WVF_PSI_KEY` config; fails clean when quota-limited
- `sites add --deep`: site-level deep flag (schema v4) — scheduler and
  scan-now honor it; `{"deep": true}` on `/api/scan` is a per-request
  override (jobs.deep)
- Lighthouse verified end-to-end: score + findings into detail_json

## 0.1.0 — 2026-11-04 (initial working system)

### Phase 0-2: scaffold + CDP metrics + HTTP checks

- Go/Cobra CLI, chromedp v0.22 (generics API, remote.WebSocket)
- Buffered-observer injection for LCP/CLS/longtask (observer-only
  entry types); nav-timing TTFB; per-response protocol/encoding/
  status/encoded-bytes + full headers; DOM/JS-heap counters
- Encoding negotiation matrix (br/gzip/zstd), TLS version, alt-svc,
  quic-go direct h3 probe (advertised vs served), pquerna/cachecontrol
  RFC 7234 verdicts per asset; per-sample cache disabled
- Verified vs local sitespeed.io run on <www.rutenskiklub.dk>:
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
- `scan --html` self-contained report (`__REPORT_DATA__` injection)

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
