# Capability matrix

Survey of the tools studied against the features wvf
implements — used to decide scope: what to reuse, what to build,
what to skip. Sources linked in `inspiration.md`.

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
