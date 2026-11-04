# Website Vitals Flow — Build Plan

Derived from `analysis.md` (§1–15). Phased so the riskiest piece —
CDP metric extraction — is proven first, persistence lands before the
UI, and every optional engine degrades gracefully.

Pre-step (per §9.0): **try sitespeed.io once before building.** If it
fits, this plan is moot. Assume it doesn't and proceed.

Default answers to open questions unless overridden:

- Schedule syntax: plain intervals (`30m`, `6h`, `daily`) — stdlib
  ticker, no `robfig/cron` for v1.
- Chrome: pinned headless-shell, managed download; `browser_version`
  stored per run.
- Samples: 3 loads, median reported; raw samples kept in `detail_json`.
- `scan` on unknown URL: one-shot only; monitoring requires
  `sites add`.
- Dashboard: bind `127.0.0.1`, no auth (v1).
- Retention: `keep_last=100` AND `max_age_days=56` on by default.
- Throttling: `provided` (unthrottled) for localhost parity;
  Lighthouse runs get the same method.

---

## Phase 0 — Scaffold (foundation only)

**Goal:** repo shape + quality gates identical to KBM-flow.

- `go.mod` (Go 1.26+), `cmd/wvf/main.go` thin, packages under
  `internal/` (cobra root command, no logic in cmd).
- `Makefile`: `check` = gofmt + vet + `go test -race` + build;
  `coverage-ci` per KBM; `node --check` gate for static JS.
- `golangci-lint` config; `.gitignore`; `AGENTS.md` noting build/test
  commands; license decision (AGPL-3.0 like KBM?).
- Config loading: `wvf.yml` (http addr, state.db path, browser path,
  defaults) + env overrides — same shape as KBM config.

**Verify:** `make check` green on an empty CLI skeleton;
`wvf --help`, `wvf version` work.

## Phase 1 — Walking skeleton: `wvf scan` (the risky part first)

**Goal:** prove CDP metric extraction end-to-end before anything else
is built.

- `internal/chrome`: locate browser (config path →
  `~/.cache/wvf/headless-shell` → system Chrome fallbacks);
  launch via chromedp ExecAllocator with `--remote-debugging-port`;
  `context.WithTimeout` everywhere; guaranteed cleanup.
- `internal/measure`: navigate → `PerformanceObserver({buffered:true})`
  for LCP/CLS/FCP/longtask → NavTiming TTFB → `Performance.getMetrics`
  → `Network` events (protocol, encoding, sizes, cache headers) →
  N=3 loads, median + raw samples.
- `wvf scan <url> --json` prints the run record. No storage yet.

**Verify:** scan `http://localhost:3000` (jekyll serve or any fixture)
→ sane TTFB/FCP/LCP/CLS/TBT; scan an h2 site → `protocol: h2` observed;
kill-on-timeout test with a hanging fixture page.

## Phase 2 — HTTP checks layer

**Goal:** the checks no engine gives us, all stdlib/lib-based.

- `internal/httpcheck`: stdlib client probe — Accept-Encoding
  negotiation matrix (gzip / br / zstd), `alt-svc` advertisement,
  redirect/TLS basics; `pquerna/cachecontrol` → per-asset RFC 7234
  reasons + expiry; merge verdicts into the run's `detail_json`.
- `internal/http3`: `quic-go` direct h3 probe — site config flag
  `h3_probe` (default on for https sites); report "advertised" vs
  "negotiated" separately.
- Compression: optional body-decode verification behind a flag using
  `andybalholm/brotli` + `klauspost/compress` (zstd) — headers+size is
  the default.

**Verify:** fixture server that speaks br but not zstd → matrix shows
br ✓/zstd ✗; `alt-svc: h3` advertised but probe fails → reported as
advertised-only, not an error.

## Phase 3 — Storage, site keys, retention

**Goal:** persistence before UI — everything after this writes real
history.

- `internal/store`: `modernc.org/sqlite`, WAL, `busy_timeout`, one
  write connection. Schema v1: `sites`, `runs` (metric columns +
  `engine` + `detail_json`), `jobs`. `user_version` migration
  harness — `CREATE TABLE IF NOT EXISTS`, additive bumps only.
- `internal/sitekey`: URL normalization → `localhost:3000`-style key;
  variants (scheme-less, trailing slash, case) collapse.
- Retention: prune `keep_last` + `max_age_days` **inside the run
  insert transaction**; `wvf prune` manual command; daily sweep in
  serve loop (lands in Phase 4).
- Commands: `sites add|list|remove`, `scan` now persists + prints
  summary; `report <id>` dumps stored run.

**Verify:** scan twice → one site row, two runs; prune with
keep_last=2 → oldest dropped atomically; migrate test on a `.backup`
copy of a live db, never the live file (KB lesson).

## Phase 4 — Jobs, scheduler, `serve`

**Goal:** the monitored mode — this is the app's core loop.

- `internal/jobs`: jobs table + single-slot runner + queue;
  `FailStaleJobs` on boot; `k/T`-style progress detail lines while a
  scan runs.
- `internal/scheduler`: per-site ticker reading `schedule` interval;
  injectable interval for tests (`runScheduler(ctx, interval)`);
  enqueues into the same runner as manual scans — one lane, two
  triggers.
- `internal/server`: stdlib `ServeMux`, `embed.FS` static dir,
  `WVF_DEV_STATIC` dev mode, JSON API (`/api/sites`, `/api/runs`,
  `/api/jobs`, `/api/scan-now`), baseline security headers, CSP
  `default-src 'self'; script-src 'self'`.
- `wvf serve` command.

**Verify:** two queued scans run serially; restart mid-scan → job
marked failed, no phantom "running"; scheduled site scans on interval
in a `synctest`-style or short-interval test.

## Phase 5 — Dashboard + report surface

**Goal:** the "run a test → get a report" UX, both surfaces.

- Vendored Bulma + Alpine **CSP build** + Chart.js via the pinned npm
  workflow (package.json + `npm ci` + sync script — KBM recipe);
  `theme.js` dark/light; one accent palette.
- Pages: landing (site list + status tiles), site detail (per-engine
  metric trend charts + run table), run detail (metrics, HTTP checks,
  HAR waterfall), jobs indicator across manual+scheduled lanes.
- `scan --html` → self-contained `report/index.html` via the
  `__REPORT_DATA__` injection pattern; `report <id> --html`
  regenerates any stored run.
- HAR: assemble from `Network` events during Phase 1 collection;
  store in run; render waterfall client-side.

**Verify:** honest empty states ("trend appears once runs accrue");
one point = one dot; alpine CSP build passes `node --check` and CSP
(no template literals / `?.`).

## Phase 6 — Deep engines (all optional, all extract-only)

**Goal:** issue-finding + debugger data without reimplementing audits.

- `internal/webvitals`: vendored `web-vitals.attribution.js` injected
  via CDP on deep runs → LCP element + sub-parts, CLS shift entries
  w/ nodes, lab-INP via `Input.dispatchMouseEvent`/`dispatchKeyEvent`.
- `internal/lighthouse`: `wvf doctor` detects `node` + `lighthouse`;
  run against the same Chrome port, `--output=json`,
  `--throttling-method=provided`; parse audits → findings in
  `detail_json`; subprocess deadline + kill. "Full audit" scan trigger
  + optional slower per-site schedule.
- `internal/psi`: opt-in per site; public FQDNs only; stores CrUX
  field percentiles tagged `engine=psi`.
- Artifacts: `Page.startScreencast` frames + `Tracing` → stored
  Perfetto-openable trace + filmstrip on deep runs.

**Verify:** deep run on a deliberately-bad fixture (unsized img,
render-blocking CSS, br-less server) → findings populated, lab-INP
present, audits parsed; run without Node → CDP-only scan, findings
say "lighthouse unavailable".

## Phase 7 — Hardening + ship

- `wvf doctor`: Chrome found/version, node+lighthouse present,
  db writable, port free, UTF-8 locale exported (GUI-spawn guard).
- Timeouts audited everywhere; `golangci-lint` + `go vet` clean;
  `go test -race` green incl. fault-injection on store (RAISE(ABORT)
  trigger pattern for mid-tx failure → no half-written state).
- README + `docs/USAGE.md`; `AGENTS.md` updated with run/verify
  commands; release build + install docs.

**Verify:** fresh machine — `wvf doctor` → guided setup → `scan` →
`serve` → schedule a site → history accrues → purge works.

---

## Order rationale

| Phase | Why now |
| --- | --- |
| 1 scan | Riskiest unknown (CDP extraction) proved before any scaffold is load-bearing |
| 2 httpchecks | Cheap, independent, completes the "every run" payload |
| 3 storage | Everything after this produces real history — no rework |
| 4 serve/scheduler | The app's reason to exist; lands on real storage |
| 5 UI/report | Cosmetic layer over working data |
| 6 deep engines | Optional by design; stubbed interfaces already return "unavailable" |
| 7 hardening | Real usage surface exists to harden |

## Dep tracker (add only at the phase that needs it)

| Dep | Phase | Required? |
| --- | --- | --- |
| cobra | 0 | yes |
| chromedp + cdproto | 1 | yes |
| pquerna/cachecontrol | 2 | yes |
| quic-go | 2 | yes (h3 probe) |
| andybalholm/brotli | 2 | optional (body verify) |
| klauspost/compress | 2 | optional (zstd) |
| modernc.org/sqlite | 3 | yes |
| (npm) bulma/alpine-csp/chart.js/web-vitals | 5, 6 | vendored assets |
| Node + lighthouse | 6 | optional capability |
| robfig/cron | — | deferred (intervals suffice) |
| x/net/publicsuffix | — | deferred (normalization edge cases) |

## Explicit non-goals (v1)

- Whole-site crawling (unlighthouse model) — per-URL targets only.
- Multi-browser (Firefox/WebKit) — Chromium only.
- Real user monitoring — lab data only (+opt-in PSI).
- Auth/multi-user — `127.0.0.1` binding only.
- Concurrent scans — single slot is a feature (memory bound).
