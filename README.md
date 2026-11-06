# website-vitals-flow

![website-vitals-flow](docs/assets/Borg-Collective_template%20website-vitals.svg)

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

Two lists: **monitored sites** (scheduled) and **recent scans** (ad-hoc
`scan url` results — promote via `edit` by setting a schedule).

![dashboard](docs/img/dashboard.png)

Site detail at `/site/{key}` — metric cards (vitals + PROTO
h1/h2/h3, COMP br/gzip/zstd, TLS, cacheable % + header-signal
checklist, page size, console), last-scan screenshot panel,
per-metric trend charts with threshold bands, findings, console
output, run history. Reports live at `/site/{key}/report/{id}`;
`?site=` / `#` deeplinks still work.

![site detail](docs/img/site-detail.png)

## API (IDE/agent-callable)

Full reference: [docs/api.md](docs/api.md) — REST endpoints, VS Code
tasks.json setup, and MCP tools (scan/history/run/prune/jobs) over
two transports:

    # preferred — through the running server (shared job queue)
    {"mcpServers": {"wvf": {"url": "http://127.0.0.1:8420/mcp"}}}

    # stdio fallback — works without a running server
    {"mcpServers": {"wvf": {
      "command": "bin/wvf", "args": ["mcp"],
      "env": {"WVF_STATE_DB": "state.db"}}}}

    POST /api/scan      {"url": "...", "wait": 90, "deep": true}  # sync or async
    GET  /api/jobs/{id}                              # poll → run_id
    GET  /api/sites/{key}/runs                       # history
    POST /api/scan-now  {"key": "..."}               # by site key

## Commands

`scan <url>` [--samples N] [--settle D] [--timeout D] [--json] [--html f] [--deep] [--psi] [--only lcp,cls,...]
`sites add|list|remove` [--schedule D] [--deep] [--only set] · `report <id>` · `prune`
`compare <site>` `[--base <run-id>]` `[--thresh %]` — baseline-vs-latest metric diff
`serve` · `doctor` · `mcp` (stdio MCP server for IDEs/agents)

### Menubar + extras

- `scripts/swiftbar/` — SwiftBar plugin (`wvf.30s.sh`): menubar status,
  running jobs, scan-all, start/stop. `install.sh`/`uninstall.sh`
  (self-verifying), `test.sh` lifecycle tests in `make check`
- `POST /api/scan-all` — scan-all / deep-scan dashboard buttons target
  every site with a schedule
- theme toggle (auto/light/dark, persisted) + en/da UI strings

### Badges & screenshots

- `GET /badges/{metric}-{band}.svg` — vendored shields.io badges
  (badge-maker, generated offline via `npm run gen:badges`)
- `GET /badge.svg?metric=lcp&value=648` or `?run=<id>&metric=cls` —
  value-bearing badges, band-colored
- `go run ./scripts/shot <url> <out.png> [wait-ms] ['js'] [height]` —
  regenerate docs/screenshots headlessly

## Measurement matrix

| Check | Default scan | `--deep` | `--only` | Source |
| --- | --- | --- | --- | --- |
| TTFB / FCP | yes | yes | `ttfb` `fcp` | CDP navigation timing |
| LCP / CLS / TBT | yes | yes | `lcp` `cls` `tbt` | PerformanceObserver |
| lab-INP (worst click) | — | yes | `inp`, `deep` | web-vitals 6.2.3 + synthetic input |
| waterfall per-request timing | yes | yes | — | CDP network events |
| protocol / encoding / TLS / redirects | yes | yes | `http` | httpcheck (net/http) |
| compression matrix br/zstd/gzip/identity | yes | yes | `http` | httpcheck Accept-Encoding probes |
| cacheability verdicts (RFC 7234) | yes | yes | `http` | pquerna/cachecontrol |
| h3 advertised + direct probe | yes | yes | `h3`, `http` | quic-go |
| image oversize / format hints | yes | yes | `images` | CDP DOM + network join |
| JS/CSS delivery budgets | yes | yes | `js`, `css` | findings (bytes+mime) |
| site basics (robots/sitemap/favicon) | yes | yes | `http` | httpcheck HEAD probes |
| vitals findings + thresholds | yes | yes | `vitals` | score bands |
| LCP/CLS/INP attribution | — | yes | `deep` | web-vitals 6.2.3 bundle |
| lighthouse audits + findings | — | yes | `deep` | lighthouse CLI (Node) |
| filmstrip (AVIF/WebP) | — | yes | `deep` | CDP screenshot + avifenc |
| CrUX field p75 + PSI score | opt-in `--psi` | — | `psi` | PSI API (needs key) |
| console warn/error/exception capture | yes | yes | — | document-start JS recorder |
| doc cache/CDN header signals | yes | yes | `http` | httpcheck (cache-control, etag, vary, expires, last-modified, age, cf/x-cache) |

`--only` filters BOTH pipeline stages and reported findings — e.g.
`scan <url> --only lcp,cls` skips http probes/h3/deep entirely.

## Reading findings by responsibility

Findings carry a category; read them through three lenses:

- **webserver/ops** — compression (`br`/`zstd`/`gzip`), cacheability,
  protocol h2/h3, TLS, redirects, headers, site-basics presence:
  things configured on the server/CDN, not in page code.
- **website/content** — images (oversize, format), CSS/JS budgets,
  DOM size, vitals (LCP/CLS/TBT), attribution: things generated by
  the site build/templates.
- **engine** — every finding is prefixed: plain ids = our checks,
  `lh_*` = lighthouse, `attr_*` = web-vitals attribution,
  `psi_*` = PageSpeed/CrUX. Trend charts never mix engines.

## Site basics & best practice

Every http-enabled scan HEAD-probes `/robots.txt`, `/sitemap.xml`,
`/favicon.ico`, `/site.webmanifest` and reports missing files as
`basics_*` info findings. Beyond those wvf deliberately does not
dictate SEO — but the checklist worth keeping: robots.txt pointing
at a sitemap, sitemap.xml present, favicon, a webmanifest for PWA
basics, and (via findings) HSTS/security headers are on the roadmap;
compression/cache/protocol coverage already lives in the server
group above.

## Inspiration

wvf takes heavy inspiration from [webvitals.dev](https://webvitals.dev/)
(CWV debugging tooling — LCP/CLS/INP attribution, threshold bands) and
[sitespeed.io](https://sitespeed.io) (repeatable median-of-N scanning,
coach-style findings, trend history), and from
[Lighthouse](https://github.com/googlechrome/lighthouse)/PSI for audits
and field data. Unlike sitespeed's Grafana/Graphite stack, wvf is a
**minimal self-hosted single binary** for local and dev-loop testing:
SQLite state, embedded dashboard, serialized scan queue, zero daemons.

## Resources & packages

Measurement: [chromedp](https://github.com/chromedp/chromedp) (CDP),
[web-vitals](https://github.com/GoogleChrome/web-vitals) 6.2.3 vendored,
[Lighthouse](https://github.com/googlechrome/lighthouse) (optional),
[PSI API](https://developers.google.com/speed/docs/insights/v5/about),
[quic-go](https://github.com/quic-go/quic-go) (h3 probe),
[cachecontrol](https://github.com/pquerna/cachecontrol) (RFC 7234).
Infra: [cobra](https://github.com/spf13/cobra),
[modernc sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure-Go).
UI: [Bulma](https://bulma.io), [Alpine CSP](https://alpinejs.dev),
[Chart.js](https://www.chartjs.org), [shields.io](https://shields.io)/
[badge-maker](https://www.npmjs.com/package/badge-maker) — all vendored,
no CDN/runtime deps. References:
[web.dev vitals](https://web.dev/articles/vitals),
[Lighthouse scoring](https://developer.chrome.com/docs/lighthouse/performance/performance-scoring),
[Chrome performance insights](https://developer.chrome.com/docs/performance/insights),
[WebPageTest](https://github.com/catchpoint/WebPageTest) (prior art).

## Quality tooling

`make check` — the full gate, run before commit: `gofmt` gate,
`go vet`, `go test -race`, `golangci-lint` (`.golangci.yml` v2),
build, `jscheck` (`node --check` on all static JS), `uiqa`
(html/css/alpine-csp hygiene — no `x-html`, no `||{}` guards, no
`\'` escapes, no external assets, balanced braces).
`make smoke` — e2e: serves on :8421 and fails on any browser console
error headlessly (needs Chrome).
`make lint` — golangci-lint alone. `build` stays compile-only.

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
