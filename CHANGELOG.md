# Changelog

All notable changes to website-vitals-flow. Format loosely follows
[Keep a Changelog](https://keepachangelog.com).

## Unreleased

## 0.2.1 — 2026-11-14

### Added

- **License**: AGPL-3.0-only (`LICENSE` + badge)
- **Semgrep config**: `.semgrepignore` (default template re-declared +
  repo scope), `make semgrep` gate joined to `check`, `semgrep ci` CI
  job (`SEMGREP_APP_TOKEN`, platform rules)
- **SonarCloud + Codecov**: `sonar-project.properties`, `sonar` CI job
  (self-skips without `SONAR_TOKEN`), tokenless codecov upload
- **Release automation**: `release.yml` — `v*` tag push verifies the
  changelog heading, runs gated `make release`, attaches 5-platform
  binaries + SHA256SUMS to the GitHub release
- README badge set (status/CI/quality/coverage/release/license),
  repo description + topics, `README.todo.swifibar.md` analysis doc

### Fixed

- **SonarCloud main branch** renamed master→main (project was
  provisioned with a nonexistent default — badges now compute and
  `main` gets full branch analysis instead of expiring SLB scans)
- **Coverage** 38%→72%: `codecov.yml` scopes out `cmd/`+`scripts/`
  (entry point + dev harness), matching `sonar.coverage.exclusions`;
  new tests — `httpcheck` via `httptest.Server` (0→86%), `findings`
  table-driven analyzer + adapters (32→98%), `jobs` runner/Persist
  (6→58%), `scheduler` sweep+ticker (24→95%), `cli` sites/prune/
  report/jobs commands (→41%). Second pass — capability-gated
  integration + fake binaries: `psi` via endpoint seam (0→91%),
  `deep` via fake lighthouse/avifenc scripts + LHR item helpers
  (0→60%), `pipeline` httptest + EngineErrors/merge (0→68%), `http3`
  early-return/unreachable probe (0→73%), `measure` netLog CDP event
  folding (0→39%), `chrome` Find/probeVersion fakes (0→53%), `cli`
  doctor/serve/mcp/version + real-chrome scan integration (→79%),
  `config` env overrides, `store` max-age retention + error paths
- `make release` now stamps `-X cli.version` into dist binaries
  (previously reported `wvf dev`); v0.2.0 assets rebuilt
- `chrome.Find` no longer returns the first candidate that merely
  exists — managed/candidate/PATH binaries must answer `--version`,
  skipping unlaunchable stubs (e.g. snap placeholders on Ubuntu CI)
  in favour of a real browser; explicit `browser_path` unchanged
- `chrome.Launch` adds `--no-sandbox` only under `$CI` — stock runners
  lack kernel user namespaces so Chrome dies silently; local launches
  keep the sandbox
- Job polling (`awaitJob`/`pollJob`) no longer surfaces
  "context canceled" tool errors when the client disconnects mid-poll
  — retries once on a fresh context and reports the job's real state
- `fmtSscanfloat` fractional parsing — `?value=0.15` badges rendered
  as 15 (the `.` never entered fractional mode); `?run=` badges were
  unaffected since those read stored metrics
- `JobState` uses fixed UPDATE statements instead of a formatted
  column name (SonarCloud S2077); `[` → `[[` in shell scripts
  (S7688); `void` on fire-and-forget dashboard promises (S9383);
  NOSONAR justifications for sitekey http default, PATH-based
  lsof/ps, and detached job-state reads
- Third test pass — coverage →~83%: `server` REST/badge/SSE/page
  handlers + card builders (56→82%, 66→89%), `cli/mcp` stdio via
  os.Pipe (0→91%), `cli/root` tree+Execute (29→86%), `jobs.
  RunSiteScan` + `mcpserver.DirectScan` gated real-Chrome
  integration (jobs 59→86%, deep 60→92%, pipeline 68→90%),
  `store` job methods, `chrome.Launch` failure path
- `buildCards` split into per-card helpers — SonarLint complexity 47→<15
- Makefile `lint`/`semgrep`/`yamlcheck` optional-tool skips — `exit 0`
  only exited the recipe line's subshell so the tool ran anyway (exit
  127 on minimal environments); all three now self-skip correctly, and
  `release.yml` runs `npm ci` so the node-backed gates execute instead
  of silently skipping on the release runner
- Docs: `jobs`/`version` commands, full `make check` gate list, `settle`
  is a scan flag not a wvf.yml key, site mutation + badge endpoints in
  api.md, License section

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
