# website-vitals-flow

Contained web-performance monitor: localhost/FQDN/IP targets, real
Chrome Core Web Vitals + HTTP checks (h2/h3, compression, caching),
monitored-site history, IDE-callable API. Single Go binary, SQLite.

## Build & verify

- `make check` — gofmt gate + vet + `go test -race` + build
- `make build` → `bin/wvf`
- `bin/wvf doctor` — engine availability (chrome/node/lighthouse)
- `bin/wvf scan <url> [--deep] [--psi] [--json] [--html f]`
- `bin/wvf serve` — dashboard + scheduler on 127.0.0.1:8420
- Chrome/Chromium auto-detected; `browser_path` in wvf.yml or
  WVF_BROWSER_PATH to override; `psi_key`/WVF_PSI_KEY for PSI
- Release: add `## X.Y.Z — <date>` heading in CHANGELOG, push main,
  tag `vX.Y.Z`, push tag — `release.yml` gates + attaches dist binaries

## Conventions

- `cmd/wvf` thin main; everything under `internal/`
- RunSiteScan (internal/jobs) is THE scan path — cli/manual/scheduled
- Schema migrations: additive `ALTER TABLE` only, bump schemaVersion,
  guarded by `v < N`; never edit old schema constants
- Retention inside the run-insert transaction — never a separate pass
- Engine tag per run; don't mix engine series on one chart
- modernc sqlite stores RFC3339 TEXT — scan as string + time.Parse
- chromedp v0.22 generics API; observer-only entry types need
  addScriptToEvaluateOnNewDocument; page JS runs on TAB contexts
- alpine-csp is a restricted parser: no object literals (||{}), no
  x-html, no unicode logic in expressions — a thrown Expression Error
  aborts the whole subtree (badges AND x-bind:title tooltips); verify
  with `make smoke`, never ship console errors
- Vendored UI assets in static/vendor via pinned package.json — never CDN
- `.semgrepignore` REPLACES Semgrep's default ignore list — defaults are
  re-declared at its top; add repo entries below. Suppress single-site
  findings inline with `// nosemgrep` + `#nosec`, never by broad ignore
- See analysis.md (design), build-plan.md (phases), matrix.md (survey)
