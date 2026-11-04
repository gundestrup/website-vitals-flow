# Website Vitals Flow — Analysis

Working analysis for a self-contained local web-vitals testing tool.
Goal restated: a webvitals.dev-style tool that runs entirely on
localhost, able to scan `localhost:<port>` targets during development
and FQDN/`IP:port` targets once deployed — measuring *changes over
time* and *finding issues*: web vitals with LCP/CLS/INP debuggers,
Lighthouse audits, PageSpeed-Insights-style data, HTTP/2-3,
compression (br/gzip/zstd), caching, and GTmetrix-style optimization
findings (images, CSS, JS). Two use modes: one-shot "run a test → get
a report", and a monitored site list scanned on schedule with history.
Strategy: extract data from established engines (Lighthouse,
web-vitals.js, optionally PSI) rather than reimplementing audits —
while staying small, stable, low memory footprint. Inspired by the
KBM-flow dashboard stack ("like our dashboard").

Sources: `inspiration.md`, engineering-memory knowledge base (KB notes
cited inline), plus a current survey of Go browser-automation options.

---

## 1. Language decision: Go (recommended) over Ruby

Recommendation: **Go**, matching the existing KBM-flow dashboard stack.

| Criterion | Go | Ruby |
| --- | --- | --- |
| Footprint | Single static binary, idle RSS ~15–40 MB | Interpreter + gems, typically 60–120+ MB for a daemon |
| Deployment | One binary; no runtime on target machine | Needs rbenv/rvm, bundler, native gems |
| Browser driving | `chromedp` / `go-rod`: pure-Go CDP clients, no extra runtime | `ferrum` (CDP) exists and is good, but Ruby side still heavier |
| SQLite | `modernc.org/sqlite` — pure Go, no cgo, proven in KBM-flow | `sqlite3` gem needs native build |
| Dashboard reuse | KBM-flow patterns copy over 1:1 (embed.FS, vendored Bulma/Alpine/Chart.js, job model, state.db) | Would be a fresh stack |
| Prior art | KBM-flow already proves the exact shape of this app | gundestrup.net / AID are Rails-scale, wrong weight class |

KB basis: `repos/Knowledge-BA46-Flow.md` — "Go 1.26, Cobra,
`modernc.org/sqlite`, stdlib `net/http` + `embed.FS`; Bulma/Alpine/
Chart.js vendored through the pinned npm workflow." That stack already
does: config files, SQLite `state.db`, background jobs with progress,
and a vendored offline dashboard. This project is structurally the same
app with a different collector.

Ruby remains viable (the KB has `whenever` for cron, `minitest`,
solid-queue lessons), but it loses on "contained + low memory" and buys
nothing Go lacks here.

## 2. The measurement & analysis engine — the one real architectural decision

**Real Web Vitals require a real browser; real issue-finding requires a
real analyzer.** There is no honest way to get LCP/CLS from an HTTP
client — and no practical way to reimplement Lighthouse/GTmetrix-grade
audits (unused CSS/JS, image optimization, render-blocking, cache
policy, TBT task breakdown) ourselves; that surface is ~100 maintained
audits. Combined with the user's directive — *prefer established
libraries/extensions and export/extract their data* — the design is a
**layered engine**: our thin CDP layer measures and checks cheaply on
every run; established engines are *run against the same Chrome and
their output extracted* for the deep/diagnostic surface.

### 2a. Requirement → source map (combining both perspectives)

| Requirement | Source | Notes |
| --- | --- | --- |
| "Normal" vitals: TTFB, FCP, LCP, CLS, TBT | Our CDP layer (buffered PerformanceObserver + NavTiming + `Performance.getMetrics`) | Cheap, runs on every scan, feeds trend charts |
| LCP / CLS debuggers (webvitals.dev-style) | `web-vitals.js` **attribution build**, vendored + injected via CDP | LCP element + sub-part breakdown (TTFB / load delay / render delay), CLS entries with offending nodes and per-shift values — the exact data those debuggers display |
| INP debugger | web-vitals.js attribution + synthetic input via `Input.dispatchMouseEvent`/`dispatchKeyEvent` | Real event-timing entries, but still synthetic — label it lab-INP, not field INP |
| Lighthouse scores + issue audits | **Lighthouse (Node)** attached to our Chrome via `--remote-debugging-port`, JSON parsed into storage | The entire "find issues" surface — ~100 maintained audits |
| GTmetrix-style optimization (images/CSS/JS) | Same Lighthouse JSON | `modern-image-formats`, `uses-optimized-images`, `uses-responsive-images`, `unused-css-rules`, `unused-javascript`, `unminified-css/javascript`, `render-blocking-resources`, `third-party-summary`, `total-byte-weight`, `dom-size`, `largest-contentful-paint-element` |
| PageSpeed Insights / CrUX field data | PSI API — **opt-in, public FQDN only** | Can't reach localhost and sends the URL to Google; gives real-user percentiles we cannot produce locally |
| HTTP/2 + HTTP/3 | `Network.responseReceived.protocol` (h1/h2/h3 observed), `alt-svc` header, optional `quic-go` direct h3 probe | "Advertised" vs "negotiated" reported separately |
| Compression (gzip / br / zstd) | Accept-Encoding negotiation probes (stdlib client) + CDP encoded-vs-decoded sizes + Lighthouse `uses-text-compression` | An explicit "server does/doesn't speak br" verdict, not inference |
| Caching | `pquerna/cachecontrol` RFC 7234 verdicts + Lighthouse `efficient-cache-policy` | Per-asset reasons + expiry, not just header echoes |
| Waterfall (GTmetrix view) | HAR assembled from our CDP `Network` events | Established export format — renders in the dashboard or external tools |
| Filmstrip / trace | `Page.startScreencast` frames + CDP `Tracing` domain | Perfetto-openable artifact stored per deep run |
| Trend: dev vs deployed over time | Same storage path for every trigger; normalized site key | One series **per engine** — see mixing caveat (2e) |

### 2b. Browser driver (unchanged)

| Option | Runtime cost | Verdict |
| --- | --- | --- |
| `chromedp` (pure-Go CDP client) | Launches/attaches to Chrome; lib itself is small | **Recommended driver.** Zero extra runtime; binary stays self-contained |
| `go-rod` | Same CDP path, friendlier API | Good alternative if chromedp ergonomics annoy |
| `playwright-go` | Ships a Node.js driver subprocess + browser binaries | Rejected — violates "contained, low memory" |
| sitespeed.io / WebPageTest | Docker stacks, hundreds of MB | Rejected — explicitly the weight class being avoided |
| HTTP-only checks (h2/h3, compression, caching) | stdlib `net/http` | **Do anyway** — near-free first-class layer |

(Full survey of "does-it-for-you" Go libraries in §11 — spoiler: none
do the analysis natively; the field is PSI-API wrappers and dead
projects.)

### 2c. Lighthouse: rejected as *the* engine, accepted as *an* engine

The first pass of this doc rejected Lighthouse outright for its Node
footprint. The expanded requirement — webvitals.dev-style issue finding
plus GTmetrix-style optimization advice — cannot be honestly delivered
without it. Revised stance:

- **Lighthouse is the established audit engine; we extract, we don't
  reimplement.** `--output=json` → parse → normalize into
  `runs.detail_json` and a findings view. Its audits *are* the
  GTmetrix-style optimization list.
- **Node is a *capability*, not a core dep.** `wvf doctor` reports
  `node`/`lighthouse` availability; scans degrade gracefully to the CDP
  layer when absent. Supply options: a system/global `lighthouse`
  (`npm i -g lighthouse`), or a small vendored Node sidecar script
  (`node scanner.mjs` with pinned `node_modules`) — either way a Node
  runtime is required; there is no maintained Go port (§11 explains
  why).
- **One Chrome serves both layers:** chromedp launches headless Chrome
  with `--remote-debugging-port=<p>`; Lighthouse attaches to the same
  port — no second browser, one consistent environment.
- **Cadence split solves the footprint:** the CDP layer is seconds and
  light enough for scheduled ticks; a full Lighthouse run is ~10–30 s
  and ~1 GB transient. Run it as "full audit" (manual button, or a
  slower per-site schedule such as daily) while metrics trend on the
  fast cadence. Idle daemon footprint stays ~tens of MB.
- **Throttling pitfall:** Lighthouse's default *simulated* throttling
  inflates timings vs. unthrottled localhost, so Lighthouse numbers and
  CDP numbers are not the same series. For dev-machine parity use
  `--throttling-method=provided` (or `devtools`) — and mark every stored
  metric with its `engine` so series never silently mix.

### 2d. Chrome sourcing

chromedp is not a bundled browser. Two sane modes:

- *System Chrome/Chromium* — zero install, but version drifts (metrics
  can shift between Chrome releases; record `browser_version` on every
  run so history stays interpretable).
- *Chrome for Testing / headless-shell* — pinned version, smaller
  (~headless-shell is significantly lighter), reproducible metrics;
  costs a managed download step (cf. `npx playwright install`, but only
  one artifact). Recommended default for trend-stable data.

### 2e. Metrics collection (CDP layer — all standard, ~300 lines)

- Inject/navigate, then read the performance timeline with
  `PerformanceObserver({buffered: true})` — returns `largest-contentful-paint`,
  `layout-shift`, `first-contentful-paint` entries even post-load
  (confirmed pattern from cdpfleet's lab-run docs).
- Navigation Timing API → TTFB (`responseStart`), DOM timings.
- `longtask` entries → **TBT** (Total Blocking Time).
- `Performance.getMetrics` over the CDP session → Chromium counters
  (JS heap, DOM nodes, layout count).
- `Network.responseReceived` → per-response `protocol` field
  (`http/1.1`, `h2`, `h3`), `content-encoding`, `cache-control`/`etag`/
  `expires`, encoded vs. decoded transfer sizes (real compression ratio),
  resource count and total page weight; same event stream also feeds
  the HAR waterfall export.

### 2f. Honest caveats to design around

- **INP cannot be measured without interaction.** Cheap runs use TBT as
  the documented proxy, labelled as such. The debugger layer can
  dispatch synthetic clicks/keys for a rough lab-INP — never presented
  as field INP.
- **HTTP/3:** Chrome rarely negotiates QUIC to localhost and needs
  `alt-svc` advertisement + valid TLS. Report two facts separately:
  "h3 advertised" (`alt-svc` header / observed `h3` protocol) vs.
  "h2/h1 negotiated". For `http://` localhost targets, HTTP version is
  almost always h1 — say so rather than failing.
- **Engine mixing:** trend series must be single-source. Lighthouse LCP
  ≠ CDP LCP under different throttling. Store `engine` (`cdp` |
  `lighthouse` | `psi`) on every run; chart and compare within one
  engine only.
- **Lab variance:** a single run is noisy. Take the median of N loads
  (N=3 default, configurable) per scan; store all raw samples in the
  run's JSON for auditability.
- **Cold vs warm cache:** decide per-site config (fresh context = cold,
  repeat visit = warm); KBM-style explicitness beats a hidden default.
- **Timeouts everywhere:** `context.WithTimeout` on the whole scan and
  per-navigation; a hung page must not wedge the single scan slot
  (KB `languages/go.md`: deadlines on anything that can block). The
  same applies to the Lighthouse subprocess — kill it on deadline, mark
  the job failed, don't leak Chrome.

## 3. Proposed architecture (mirrors KBM-flow)

Single Go binary, Cobra commands:

- `wvf scan <url>` — one-shot: measure, persist run under the site key,
  print summary; `--html` writes a self-contained report
  (KB `patterns/static-html-report.md`: template + `__REPORT_DATA__`
  injection, `</` escaping — this is exactly "run a test get a report").
- `wvf serve` — the dashboard: site list, per-site history/trends,
  "Scan now" button, scheduler loop, scan queue. The two modes share the
  **same scan function and the same storage path**, so a "scan now" and a
  scheduled run are indistinguishable in history except a `trigger`
  field (`manual` | `scheduled` | `cli`) — directly answering the
  requirement that scan-now results be filed under `localhost:XXX`.
- `wvf sites add|list|remove` — manage the monitored set.
- `wvf prune` — manual retention enforcement (also runs automatically,
  see §5).

Frontend: same recipe as KBM-flow — stdlib `net/http.ServeMux`,
`//go:embed` static dir, vendored Bulma + Alpine **CSP build** +
Chart.js, `default-src 'self'; script-src 'self'` CSP, dark/light via
`data-theme`, `WVF_DEV_STATIC=<dir>` dev loop (copy of `KBM_DEV_STATIC`,
KB lines 193–206). Pages: landing/site list, per-site detail (metric
trend charts + run table), run detail/report view, jobs indicator.

## 4. Storage model (SQLite, WAL)

`modernc.org/sqlite` — pure Go, no cgo, keeps the single-binary promise.
WAL mode + `busy_timeout`, one writer (KB lesson
`concurrent-hook-runs-lock-test-db.md`: SQLite allows one writer;
contention surfaces as `database is locked` or silent stalls — keep a
single write pool/connection and funnel all writes through it).

Sketch (columns to be refined at build time):

- `sites(id, key, url, label, schedule, keep_last, max_age_days,
  warmup, created_at)` — `key` is the normalized identity
  (`localhost:3000`, `staging.example.com`, `192.168.1.10:8080`).
- `runs(id, site_id, started_at, finished_at, trigger, status, error,
  browser_version, samples, ttfb_ms, fcp_ms, lcp_ms, cls, tbt_ms,
  http_protocol, page_bytes, requests, detail_json)` — headline metrics
  as columns (cheap chart queries, no JSON parsing), full payload
  (headers, resource breakdown, raw samples) in `detail_json`.
- `jobs(id, kind, site_id, state, detail, queued_at, started_at,
  finished_at)` — the KBM ops-table pattern for queue/progress.

**Site identity normalization matters:** `localhost:3000`,
`http://localhost:3000`, `localhost:3000/` must collapse to one key,
else history splits. Normalize at input (scheme default `http://`,
strip trailing slash, lowercase host, keep explicit port; default ports
80/443 kept in key only if non-standard display is wanted).

## 5. Scheduling + retention

**Scheduler:** in-process loop inside `serve` (ticker per site reading
`schedule` — plain interval like `6h`/`30m`, or cron expr via
`robfig/cron` *if* cron syntax is actually wanted; an interval keeps the
dep count at zero and covers the stated need). Design notes:

- Injectable interval in the runner (`runScheduler(ctx, interval)`) so
  tests can shrink it — KBM's `runHeartbeat` convention.
- At startup, mark any `running` job stale/failed (`FailStaleJobs`
  pattern) — a crash mid-scan must not show a phantom running job.
- One scan slot: scheduled and manual scans enqueue; the UI must show
  the queue across both lanes — KB lesson
  `kbm-ui-progress-and-help-patterns.md`: "single-job-slot backends must
  expose cross-lane job visibility or users see 'idle' while ops are
  rejected."
- OS-level alternative is free: single binary means `cron`/`launchd`
  can just run `wvf scan <key>` — worth a docs note, but the in-app
  scheduler is the primary UX.

**Retention — exactly what the KB prescribes:** per-site `keep_last`
(count cap, e.g. 100) **and/or** `max_age_days` (e.g. 8 weeks). Apply
pruning **in the same transaction that inserts a new run**, plus a
daily sweep for age-based expiry. Basis —
`lessons-learned/timeseries-needs-real-history.md`:

- Append one immutable row per run; never overwrite a "latest" snapshot
  (a chart from current-state-only storage is fabricated history).
- Prune to newest N inside the insert transaction — retention can't
  drift or race.
- Additive schema via `CREATE TABLE IF NOT EXISTS` + `user_version`;
  an older binary must refuse a newer schema by design.
- UI honesty: charts only show points that exist; "trend appears once
  runs accrue" — one point renders as one dot.
- Test migrations on a `.backup` copy of the live DB, never the live
  file (WAL pages are lost with a plain file copy).

## 6. Report surface

- CLI: `wvf scan --html` → static `report/index.html` via the injected-
  JSON pattern; `wvf report <run-id> --html` regenerates any stored run.
- Dashboard: run detail page renders `detail_json` — metrics,
  debugger attribution (LCP element, CLS shift list, lab-INP), audit
  findings grouped by category, HTTP checks, and a HAR waterfall;
  per-site page draws Chart.js trends per metric **per engine** (share
  the `lineChartOptions()` / `chartCssVar` chrome from KBM so both tools
  look like siblings).
- Score semantics: optionally compute a lab score, but lead with the
  raw metric values + thresholds (LCP ≤2.5 s, CLS ≤0.1, TBT ≤200 ms) —
  transparent and stable; a synthetic score invites argument.

## 7. Pitfalls carried over from the knowledge base

Directly applicable KB lessons, condensed:

1. **Real browser or nothing** for LCP/CLS — and Chrome version drift
   moves metrics; pin headless-shell or record `browser_version`.
2. **SQLite single writer** — one write connection, WAL, busy_timeout;
   enqueue scans, never parallel-write
   (`concurrent-hook-runs-lock-test-db.md`).
3. **History must be real appended rows**, pruned inside the insert
   transaction (`timeseries-needs-real-history.md`).
4. **Single-slot job visibility** — merge manual + scheduled lanes in
   the UI; emit `k/T`-style progress detail while a scan runs
   (`kbm-ui-progress-and-help-patterns.md`).
5. **Vendored assets only, CSP `default-src 'self'`** — no CDN at
   runtime; Alpine CSP build forbids template literals and `?.`
   (`repos/Knowledge-BA46-Flow.md`, `chartjs-in-alpine-csp.md`).
6. **go:embed is compile-time** — asset edits need rebuild unless
   `WVF_DEV_STATIC` dev mode is on (`alpine-expressions-cant-reach-globals.md`).
7. **Atomic config writes** (`atomicWrite`) and `os.Root`-style path
   confinement if report output lands on disk — same KBM stance.
8. **Deadline everything**: per-navigation timeout, whole-scan timeout,
   DB ops via `context.Context` (`languages/go.md`).
9. **`node --check`** as a dependency-free syntax gate on the vanilla
   JS; `golangci-lint` + `go test -race` for Go
   (`tools/golangci-lint.md`, `languages/go.md`).
10. **Dependency minimalism**: KBM runs on four thin deps and rejected
    the official SDK that carried server-side advisories — apply the
    same bar here; `robfig/cron` is the only plausible extra dep and
    only if cron syntax is required.
11. **macOS spawn environment**: if the binary is ever launched from a
    GUI/launchd context rather than a shell, export a UTF-8 locale —
    spawned tools hit `invalid byte sequence in US-ASCII` otherwise
    (`spawned-tools-need-utf8-locale.md`).

## 8. Rejected alternatives (recorded, KBM-style)

- sitespeed.io, WebPageTest, GTmetrix-likes: correct scope wrong weight
  — multi-container stacks, hundreds of MB, aimed at CI fleets.
- Lighthouse as *the only* engine for every scan: Node + ~10–30 s/1 GB
  transient per run is the wrong weight for a scheduled tick. Accepted
  instead as the optional deep-audit engine (§2c) — extraction target,
  not reimplementation target.
- playwright-go: needs a Node driver subprocess — one runtime too many
  for a "contained" tool. (Playwright stays right for *browser tests of
  this tool itself*, as in the jekyll gems — dev-time dep only.)
- External APIs (PageSpeed Insights) as a core path: can't reach
  localhost, sends URLs to Google — contradicts "local testing prior to
  deployment". Reclassified as an **opt-in field-data source** for
  public FQDN targets only (§2a), never required for the tool to work.

## 9. Open questions for the user

0. **Buy-vs-build checkpoint (new §12):** run `sitespeed.io` or
   `@lhci/server` once against a real target before building anything —
   if the fleet weight is acceptable, the tool already exists.
1. Schedule syntax: plain intervals (`30m`, `6h`, `daily`) enough, or is
   cron expression support (`robfig/cron`, +1 dep) wanted?
2. Chrome: pinned **headless-shell download** (reproducible, managed)
   vs **system Chrome** (zero-install, drifting metrics)? Or config
   option for both?
3. Repeat loads per scan — default 3 with median?
4. Should `scan` on a never-seen URL auto-register it as a monitored
   site, or stay one-shot unless added to the list?
5. Any authentication on the dashboard (bind to `127.0.0.1` only is the
   simple answer — recommend that plus no auth for v1)?
6. Retention defaults: keep_last 100 **and** max_age 8 weeks both on by
   default, or opt-in?
7. **Lighthouse dependency stance:** required install vs. optional
   capability? (Analysis recommends optional: `wvf doctor` reports
   availability, scans degrade to CDP-only. Making it required trades
   containment for guaranteed audit depth.)
8. Deep-audit cadence: manual-only, or per-site second schedule
   (e.g. metrics hourly, full audit daily)?
9. PSI opt-in for public sites — include it at all, or stay 100% local?
10. Throttling policy: unthrottled `provided` for localhost parity
    (recommended) vs. Lighthouse's simulated throttling for
    field-comparable numbers — or both as a per-site setting?
11. Whole-site crawl (find pages, audit each — the unlighthouse model)
    or strictly per-URL targets for v1?

## 10. Suggested shape in one paragraph

One Go binary (`wvf`) with Cobra: `scan` (one-shot + HTML report),
`serve` (embedded Bulma/Alpine-CSP/Chart.js dashboard + interval
scheduler + single-slot scan queue), `sites`, `prune`, `doctor`. One
headless Chrome (chromedp, pinned headless-shell preferred) feeding a
**layered engine**: our ~300-line CDP layer for cheap metrics + HTTP
checks (protocol, compression negotiation, RFC 7234 caching) on every
run; vendored `web-vitals.js` attribution for the LCP/CLS/INP debuggers;
optional **Lighthouse (Node)** attached to the same Chrome for the
issue/optimization audits; optional PSI for public field data; HAR +
trace as stored artifacts. SQLite (`modernc.org/sqlite`, WAL) with
`sites`, `runs` (metric columns + `detail_json`, `engine` tagged),
`jobs`; retention pruned in the insert transaction; all history under
normalized site keys like `localhost:3000` regardless of trigger.
Essentially "KBM-flow with a CDP collector" — same proven chassis, and
the analysis surface borrowed from established engines instead of
reinvented.

## 11. Go library survey — what's out there vs. what we build (2026-11)

Question asked: *how many Go libs exist that do the analysis for us?*
Honest answer: **zero that do it natively and locally.** ~10 candidates
promise "web vitals" — all are either external-API wrappers, dead, or
heavier than the thing we're building. What remains is a solid set of
building blocks that remove plumbing, while the metrics-extraction
core (~300 lines of CDP + injected JS) stays ours.

### Tier 1 — "does the analysis" candidates (all disqualified)

| Module | What it is | Why rejected |
| --- | --- | --- |
| `google.golang.org/api/pagespeedonline/v5` | Official PSI API client | External Google API — cannot reach localhost; sends URLs out; maintenance mode |
| `hugo-cachon/vitals` | Cobra CLI wrapping PSI | Same external-API problem; stale (v0.0.0, 2024) |
| `g0rbe/go-google` | PSI API wrapper | External API |
| `ncosentino/google-psi-mcp` | Go MCP server → PSI + CrUX | External API — but worth a look as architecture reference (native Go binary, keeps lab vs field data separate) |
| `foomo/pagespeed_exporter` | Prometheus exporter → PSI | External API; it's monitoring plumbing, not an analyzer |
| `websu-io/websu` | "Lighthouse-as-a-Service" REST API in Go | Abandoned (last commit 2022) and wraps the **Node** lighthouse anyway — heavyweight |
| `shyim/sitespeed.io-api` | Go API spawning sitespeed.io Docker containers | The Docker-fleet weight class we explicitly avoid |
| `meysam81/scry` | Go CLI site auditor, 94 checks, young | Explicitly **browserless** → no LCP/CLS possible; still useful as a checklist of HTTP-level audits to mirror |
| `gleicon/browserhttp` | chromedp-backed `http.Client` with `GetPerformanceMetrics`, SEO/TLS/CSP checks | Closest to batteries-included, but nav-timing only, no CWV; small/new (2025). Pattern source, not a dep |
| `aliwatters/rod-mcp` | go-rod MCP server for AI agents | Its `rod_performance` tool is a working CWV-over-rod extraction → **reference implementation to copy**, not a library to import |
| `anatolykoptev/go-browser`, `plexusone/w3pilot` | Abstraction layers over rod/CDP (BiDi+CDP, pooling, stealth) | More machinery than one-URL-at-a-time scanning needs |

### Tier 2 — building blocks worth depending on

| Module | Job it removes | Verdict |
| --- | --- | --- |
| `chromedp/chromedp` + `chromedp/cdproto` | CDP transport, generated `performance`/`network`/`page` domains | **Core dep** (decided in §2) |
| `go-rod/rod` | Same job, friendlier API | Backup if chromedp ergonomics grate |
| `pquerna/cachecontrol` | RFC 7234 cacheability verdicts — returns *reasons* a response isn't cacheable + expiry | Worth it; writing correct cache analysis by hand is fiddly |
| `quic-go/quic-go` | Real HTTP/3 probes (direct h3 request, not just `alt-svc` sniffing) | Optional/defer — h3 on localhost is rare; report `alt-svc` first |
| `golang.org/x/net/publicsuffix` | eTLD+1 for site-key normalization edge cases | Cheap, take it if normalization needs it |
| `andybalholm/brotli` | Brotli body decoding | Skip — CDP reports encoded+decoded sizes already |
| `robfig/cron` | Cron-expression scheduling | Only if cron syntax is wanted (open question §9.1) |
| `hdrhistogram` / `tdigest` | Latency distributions | Overkill — median of N samples suffices |
| `spf13/cobra`, `modernc.org/sqlite` | CLI + storage chassis | Already decided (§1) |

### What stays ours either way

- The **cheap metrics layer**: navigate → `PerformanceObserver({buffered:true})`
  read of LCP/CLS/FCP/longtask → Navigation Timing TTFB →
  `Network.responseReceived` (protocol, encoding, sizes, cache headers) →
  `Performance.getMetrics` → HAR. No lib packages this for Go because
  it's ~300 lines of CDP calls + one injected JS blob.
- The **extraction/normalization layer**: parse Lighthouse JSON,
  web-vitals.js attribution output, and PSI responses into our run
  schema — the "combine" work, which is also what lets every engine's
  results share one history and one report format.
- Reference code exists to crib from: `rod-mcp`'s `rod_performance`,
  cdpfleet's lab-run recipe, chromedp's examples; `scry`'s check list
  for HTTP-level audit ideas; `google-psi-mcp` for clean
  lab-vs-field separation.

**Bottom line for deps:** chassis (cobra, modernc sqlite) + chromedp +
cachecontrol + (maybe) publicsuffix/cron ≈ **4–6 runtime Go deps** —
inside the KBM minimalism bar. The audit *depth* comes from established
external engines (Lighthouse/web-vitals.js/PSI) consumed as data
exports — required for the issue-finding scope, kept optional so the
binary still works alone. Every "full analyzer in pure Go" option
failed the contained/local requirement.

## 12. Existing self-hosted options — build vs buy (2026-11)

Question asked: *is there an already self-hosted option available?*
**Yes — and the honest answer is sitespeed.io.** It is the established
best-practice self-hosted stack for almost exactly this job:
Browsertime drives real Chrome, Coach supplies the GTmetrix-style
audits, PageXray turns HAR into waterfalls/summaries, and
Graphite/InfluxDB + Grafana provide the trend dashboards. Cron handles
scheduling. It covers roughly 80% of the requirement list out of the
box — at the cost of being precisely the Docker fleet + ops weight
this project set out to avoid.

| Option | What it is | Covers | Misses | Weight |
| --- | --- | --- | --- | --- |
| **sitespeed.io** | The reference self-hosted perf stack (Docker compose) | CWV + Speed Index + video/filmstrip, Coach audits (image/CSS/JS advice), HAR waterfall, h2 metrics, Grafana trends, cron scheduling — **and real attribution data** (see below) | Dedicated interactive debugger UI, explicit h3 probing, Accept-Encoding negotiation matrix, per-site-key simplicity + keep-N retention, low footprint | 5+ containers; Grafana + Graphite |
| **Lighthouse CI server** (`@lhci/server`) | Node + SQLite/PG dashboard for LH history | LH audits, per-URL trend charts, build-to-build diffs, budgets/assertions | Push model — scheduling is cron'd `lhci collect`; commit-oriented, not site-oriented; weak default auth; no debugger views or HTTP matrix | Node server + Chrome; medium |
| **Checkmate** | Self-hosted uptime + pagespeed monitor (Docker, AGPL) | Site list, scheduled scans, LH scores + CWV history, uptime/SSL/infra checks | Audit/issue detail beyond scores+metrics, debugging surface | ~1 GB, compose |
| **Garie** | Docker plugin collectors → InfluxDB → Grafana | LH + PSI + Browsertime, cron, dashboards, videos | Aging project; same fleet weight | Multi-container |
| **WebPageTest private** | The original enterprise stack | Best-in-class waterfalls/filmstrips/throttling/multi-location | Ops complexity, agents — heavy overkill for local dev | Heavy |
| **k6 browser** (+Prom/Grafana) | Single Go binary collector emitting `web_vitals` metrics | Single binary(!), LCP/CLS/FCP/TTFB into a TSDB pipeline | No audits/issue-finding, scheduling external, no bundled dashboard | Binary + external TSDB |
| **PerfKit** | Local Node tool: LH + waterfall + filmstrip + SQLite trends | Proves exactly the model we want — but Windows-centric, young | Portability/maturity | Node app |
| **RUM tools** (betterlytics, next-cwv-monitor, …) | Real-user field data after deploy | The *field* half PSI would supply | Not lab testing — needs real traffic | n/a |

**sitespeed.io debugger check (verified against its docs):** it does
*not* ship webvitals.dev-style interactive debuggers, but it captures
nearly the same attribution *data* — which is what actually matters:

- **LCP** → highlighted-element screenshot (`--browsertime.screenshotLCPColor`),
  element metadata (`tagName`/`className`/full `domPath`), the same
  4-phase breakdown web-vitals.js attribution uses (TTFB / resource
  load delay / resource load duration / element render delay), and
  the LCP resource highlighted in the waterfall.
- **CLS** → per-shift entries with the shifted element's identity,
  highlighted in a screenshot above a configurable threshold
  (`--browsertime.screenshotLSLimit`, default 0.01). Same caveat as
  everywhere: the API names the element that *shifted*, not the one
  that *pushed* it.
- **INP** → measurable but **requires scripted interaction** — its
  mature Action API (`commands.navigate` → `click` → `measure.stop`)
  collects top-10 interaction latencies via the Event Timing API plus
  `eventTarget` selector, `loadState`, and `domPath`. That's
  attribution-grade INP data — but you must write a script per
  interaction; there is no "click anything, see INP" recorder.
- Plus filmstrip video, screenshots, HAR waterfalls, and a Lighthouse
  plugin — the full diagnostic toolbox in one run.

That narrows the real gap to: fleet weight, localhost-first UX,
unified per-site-key history with explicit retention, the HTTP-level
checks (h3 probe, br/zstd negotiation matrix), and a dedicated
debugger UX layered on data sitespeed already collects.

**Inspiration post folded in** (daveashworth.co, "Best Free Page Speed
& CWV Checker Tools"): its four-tool workflow maps cleanly onto the
design — PSI = the field-data entry point (our opt-in PSI layer),
GTmetrix/WebPageTest = the "why is it slow" investigation layer (our
audits + waterfall + debuggers), Pingdom = multi-location checks
(irrelevant localhost; one line for deployed-only if ever wanted). Its
key discipline points reinforce decisions already made: *lab ≠ field*
(keep engines/series separate), *never judge a single run* (median of
N), *tools score differently* (per-engine `engine` tag, don't merge
trend lines).

**What none of them gives you (the actual gap):**

- contained single binary, low memory, localhost-first UX
- per-site-key (`localhost:3000`) history with explicit
  keep-N/keep-weeks retention semantics
- h3 direct probing + Accept-Encoding negotiation matrix as
  first-class checks
- a dedicated interactive debugger UX (though sitespeed's
  attribution data covers most of what a debugger needs to show)
- one daemon that does scan-now + scheduling + retention + reports

**Decision frame:**

- *Docker fleet acceptable?* → run **sitespeed.io**. Mature, covers the
  audits + trends + waterfalls today; don't rebuild it. Cost: ~1–2 GB
  and a compose stack to babysit.
- *Audits + history are the point, fleet isn't?* → **LHCI server** +
  cron'd `lhci collect`. Much smaller surface; commit-centric model is
  the friction (its "builds" key on git commits, not calendar scans).
- *"Contained, low-memory, localhost-first" is a hard requirement* →
  the wvf design stands; the gap list above is its justification. Note
  the layered engine deliberately borrows the *same* established
  engines these products run (Lighthouse, web-vitals.js) — the build is
  orchestration + storage + UX, not analysis.
- *Hybrid fallback* → wvf can treat a local sitespeed/LHCI as an
  output target later; nothing blocks it.

**Recommendation:** spend 30 minutes running `sitespeed.io` (and/or
`@lhci/server`) against a real localhost target first. If it fits —
skip the build entirely. If it's too heavy or the missing checks
matter, that hands-on experience *is* the evidence that wvf earns its
existence. The industry pattern is collector → time-series DB →
dashboards; wvf is that shape compressed into one binary.

## 13. Capability matrix — every tool & library found (2026-11)

Legend: **●** native/full · **◐** partial or possible-with-work ·
**○** not provided · **—** not applicable

### Matrix A — complete tools, engines & services

| Tool | CWV metrics | INP | Attribution / debug data | Issue audits (img/css/js) | HTTP/2-3 | Compression | Caching | Waterfall | Filmstrip / video | History / trends | Scheduling | Localhost | Field data | Footprint |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **sitespeed.io** | ● | ◐ scripted Action API | ◐ element+domPath+sub-parts, highlight shots | ● Coach + LH plugin | ◐ h2 yes, h3 no | ◐ reports encodings | ◐ HAR headers | ● | ● | ● Grafana | ◐ external cron | ● | ○ | heavy (5+ containers) |
| **Lighthouse CI server** | ● | ◐ TBT only | ◐ LCP-element + layout-shift audits | ● all LH audits | ◐ uses-http2 | ◐ uses-text-compression | ● cache-policy audit | ◐ network detail | ◐ report filmstrip | ● | ○ push only (`lhci collect`) | ● | ○ | Node + DB |
| **Checkmate** | ● | ◐ TBT | ○ | ◐ scores + metrics only | ○ | ○ | ○ | ○ | ○ | ● | ● built-in | ● | ○ | ~1 GB compose |
| **Garie** | ◐ | ○ | ○ | ◐ LH reports stored | ○ | ○ | ○ | ◐ | ◐ videos | ● Grafana | ● cron plugins | ● | ◐ PSI plugin | multi-container |
| **WebPageTest private** | ● | ◐ scripted | ◐ element-level filmstrip/waterfall | ◐ opt checks (LH optional) | ◐ | ◐ | ● | ● best | ● best | ◐ per-test mostly | ◐ | ● | ○ | heavy ops |
| **k6 browser + TSDB** | ● | ◐ | ○ | ○ | ○ | ○ | ○ | ○ | ○ | ● Prom/Grafana | ◐ external | ● | ○ | Go binary + TSDB |
| **PerfKit** | ● | ◐ | ○ | ◐ LH audit list | ○ | ○ | ○ | ● | ● | ● SQLite | ○ manual | ● | ○ | Node app |
| **Unlighthouse** | ● | ◐ | ◐ | ● LH per crawled page | ○ | ○ | ○ | ◐ | ◐ | ◐ recent scans | ○ | ● | ○ | Node CLI |
| **PSI API** (SaaS) | ● | ● CrUX+lab | ◐ LH attribution audits | ● | ◐ | ◐ | ◐ | ◐ | ◐ | ○ | ○ | ○ | ● CrUX | external |
| **GTmetrix** (SaaS) | ● | ◐ | ◐ | ● | ◐ | ◐ | ◐ | ● | ● | ◐ history w/ account | ◐ monitored | ○ | ○ | external |
| **Pingdom** (SaaS) | ◐ shallow | ○ | ○ | ◐ | ○ | ◐ | ◐ | ● | ○ | ● | ● | ○ | ○ | external |
| **webvitals.dev** (browser tool) | ● | ● | ● reference debuggers | ○ | ○ | ○ | ○ | ○ | ○ | ○ | ○ | ◐ runs in your browser | ○ | website |
| **RUM tools** (betterlytics, cwv-monitor) | ● real users | ● | ◐ percentiles | ○ | — | — | — | — | — | ● | ● | — | ● the point | ClickHouse fleets |
| **Lighthouse** (engine, Node) | ● | ◐ TBT proxy only | ◐ LCP-element + layout-shift audits | ● the audit engine itself | ◐ uses-http2 | ◐ uses-text-compression | ● cache-policy audit | ◐ network detail | ◐ thumbnails+shot | ○ | ○ | ● | ○ | Node + Chrome |
| **web-vitals.js** attribution (engine) | ● | ● w/ input | ● the debugger data source | ○ | ○ | ○ | ○ | ○ | ○ | ○ | ○ | ● | ○ | tiny JS lib |
| **Browsertime** (sitespeed engine) | ● | ◐ scripted | ● element/domPath/sub-parts/highlights | ○ Coach does audits | ◐ | ◐ | ◐ | ● HAR | ● video | ◐ | ○ | ● | ○ | Node engine |
| **Coach + PageXray** (sitespeed engines) | — | — | — | ● advice + asset analysis | ◐ | ◐ | ◐ | ● HAR→summary | — | — | — | ● | — | Node libs |
| **wvf (proposed design)** | ● | ● synthetic lab-INP | ● web-vitals.js attribution | ● LH JSON when present | ● observed + h3 probe | ● negotiation matrix | ● RFC 7234 reasons | ● HAR | ◐ screencast/trace | ● SQLite + keep-N/age | ● in-process | ● | ◐ PSI opt-in | 1 binary, tens of MB idle |

### Matrix B — Go modules (building blocks only)

| Module | Browser drive | Vitals extraction | Audit / issue engine | HTTP-level checks | Scheduling | Storage / CLI | Extra runtime | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `chromedp` + `cdproto` | ● pure-Go CDP | ◐ enables our layer | ○ | ◐ Network domain | — | — | Chrome only | **core dep** |
| `go-rod/rod` | ● pure-Go CDP | ◐ enables our layer | ○ | ◐ | — | — | Chrome (auto-dl) | alt driver |
| `playwright-go` | ● | ◐ | ○ | ◐ | — | — | **Node driver subprocess** | rejected |
| `quic-go/quic-go` | — | — | — | ● real h3 probes | — | — | none | optional, defer |
| `pquerna/cachecontrol` | — | — | ◐ RFC 7234 reasons | ● | — | — | none | worth it |
| `andybalholm/brotli` | — | — | — | ◐ decode only | — | — | none | skip — CDP gives sizes |
| `x/net/publicsuffix` | — | — | — | ◐ eTLD+1 | — | — | none | if normalization needs |
| `robfig/cron` | — | — | — | — | ● cron exprs | — | none | only if cron syntax |
| `hdrhistogram`/`tdigest` | — | ◐ stats | — | — | — | — | none | overkill — median ok |
| `spf13/cobra` | — | — | — | — | — | ● CLI | none | chassis |
| `modernc.org/sqlite` | — | — | — | — | — | ● storage | none (pure Go) | chassis |
| `meysam81/scry` | — | ○ browserless | ◐ 94 HTTP-level checks | ● | — | ● CLI | none | checklist reference |
| `gleicon/browserhttp` | ● chromedp-backed client | ◐ nav timings | ◐ SEO/TLS/CSP checks | ◐ | — | — | Chrome | pattern source |
| `aliwatters/rod-mcp` | ● via MCP | ● working CWV impl | ○ | ◐ | — | — | Chrome | **reference impl to crib** |
| `anatolykoptev/go-browser` | ● rod/remote wrapper | ○ | ○ | ○ | — | — | Chrome | more than needed |
| `plexusone/w3pilot` | ● BiDi+CDP | ◐ | ○ | ◐ | — | — | Chrome | more than needed |
| `websu-io/websu` | — | ◐ | ◐ via Node LH | ○ | ● | ● | **Node lighthouse** | dead 2022 |
| `shyim/sitespeed.io-api` | — | ◐ | ◐ via sitespeed | ○ | ● | ● | **Docker fleet** | wrapper, not engine |
| PSI clients ×4 (`pagespeedonline/v5`, `hugo-cachon/vitals`, `g0rbe/go-google`, `google-psi-mcp`) | — | ◐ lab+field | ◐ via PSI | ○ | — | — | **external API** | opt-in field source only |
| `foomo/pagespeed_exporter` | — | ◐ | ○ | ○ | ◐ Prom scrape | — | external API | plumbing, not analyzer |

### Reading the matrix

- The **only** rows covering vitals *and* audits *and* trends are the
  fleet products (sitespeed.io, LHCI, WebPageTest) — and none covers
  the HTTP-check columns fully or ships light.
- Every Go module row is plumbing, not analysis — confirming §11:
  no native-Go analyzer exists; the audit surface must come from
  established engines (Lighthouse / web-vitals.js / Browsertime's
  scripts / PSI), consumed as extracted data.
- The `wvf` row shows the design intent: the CDP layer's cheap
  coverage on every run + engine exports for depth — the one row that
  is both self-contained and complete, at the price of building the
  orchestration/storage/UX ourselves.

## 14. Stack check — the distilled pick list (2026-11)

User's proposed engine set: **Lighthouse + web-vitals.js + quic-go +
cachecontrol + brotli** feeding one dashboard. Verdict: **yes — that
covers ~90–95% of the measurement/analysis surface, with one essential
addition and two scope notes.**

Confirmed picks:

- **Lighthouse (Node)** → audits + scores + lab vitals — the entire
  issue-finding surface, consumed as JSON.
- **web-vitals.js attribution** → LCP/CLS/INP debugger data (INP via
  synthetic input dispatch).
- **quic-go** → real h3 probing (`alt-svc` alone only proves
  "advertised"; a direct QUIC request proves "served").
- **pquerna/cachecontrol** → RFC 7234 cacheability reasons + expiry —
  correct cache analysis without hand-rolled parsing.

Corrections / additions:

1. **`chromedp` is the missing mandatory piece — list it first.**
   web-vitals.js injection, the cheap metrics layer, HAR assembly,
   `Network` protocol/encoding/sizes, `Performance.getMetrics`, and
   the synthetic input for lab-INP all hang off a CDP driver.
   Lighthouse can't substitute — it runs its own session and exposes
   none of these hooks; our Chrome + CDP is what everything else
   plugs into.
2. **brotli scope.** Only needed if the stdlib negotiation probe
   *decodes bodies* to verify compression beyond headers + wire size —
   CDP already shows the real negotiated encoding and encoded/decoded
   sizes, so headers may suffice. Keep it if body-level verification is
   wanted. If **zstd** is in scope ("br, gzip, zstd, …"), add
   `klauspost/compress` — stdlib speaks gzip only.
3. **The remaining ~5–10% is code, not libraries:** scan queue +
   single-slot runner, site-key normalization, SQLite schema +
   retention-in-insert-tx, HAR assembly (~200 lines off Network
   events), the scheduler ticker, and the dashboard itself (net/http +
   `embed` + vendored Bulma/Alpine-CSP/Chart.js). No library provides
   the product layer — that gap is precisely the build.
4. **Non-Go runtimes still apply:** Chrome (pinned headless-shell
   preferred) for everything browser-side; Node for Lighthouse — keep
   both as detected capabilities reported by `wvf doctor`, degrading
   to CDP-only scans when absent.

Final dependency shape: **chromedp + cachecontrol + quic-go +
brotli(optional) + klauspost/compress(optional, zstd) + cobra +
modernc sqlite** ≈ 5–7 Go deps, plus Chrome and optional Node —
delivering the full matrix while the daemon itself stays contained.

## 15. Footprint comparison — is it actually lightweight? (2026-11)

Yes — **idle footprint is tens of MB vs sitespeed.io's >1 GB resident.**
The honest distinction is *resident* vs *transient* memory:

| | Idle / resident | During a cheap scan | During a deep audit | Disk |
| --- | --- | --- | --- | --- |
| **wvf** | ~15–40 MB (single Go binary + in-process SQLite) | +~200–500 MB headless-shell Chrome, freed when scan ends | +~600 MB–1.5 GB Chrome + Node/Lighthouse, freed on completion | ~20 MB binary + SQLite (MBs) + one-time headless-shell (~150–300 MB) + optional node_modules |
| **sitespeed.io** | **>1 GB resident, always** — Grafana + Graphite/InfluxDB + collector containers run 24/7 | same Chrome class inside the fleet | same | several GB of images + TSDB data |
| **LHCI server** | ~100–300 MB Node daemon | — | +Chrome + LH run | Node_modules + SQLite/PG |

Key mechanics that keep wvf light:

- The >1 GB sitespeed stack is memory you're paying **around the
  clock**, whether or not a test is running — Grafana + Graphite are
  resident dashboards/TSDBs. wvf replaces both with SQLite +
  embedded static UI: near-zero idle cost.
- wvf's heavy memory is **transient and bounded**: the single-slot
  scan queue guarantees at most one Chrome (and at most one
  Lighthouse subprocess) exists at a time, and both are killed on
  completion/deadline. Between scans the daemon returns to baseline.
- Trade accepted: during those ~10–30 s a deep audit transiently
  matches sitespeed-class memory. That's the price of running real
  Chrome anywhere — sitespeed pays it the same way, per run.
- The cheap layer is what runs on schedule: interval ticks use the
  CDP scan (~Chrome only), so even scheduled operation stays far
  below the fleet's idle cost.

Verdict: **lightweight in the sense that matters** — resident memory
~25–50× lower than the docker stack, heavy cost only while a scan is
actually running, and that cost is strictly serialized.
