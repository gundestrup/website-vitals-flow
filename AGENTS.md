# website-vitals-flow

Contained localhost web vitals tester. Go 1.27, Cobra CLI, chromedp,
modernc.org/sqlite (planned), stdlib net/http + embed dashboard (planned).

## Build & verify

- `make check` — gofmt gate + vet + `go test -race` + build + node --check
- `make build` → `bin/wvf`
- `bin/wvf scan <url> [--json] [--samples N]` — one-shot measurement
- `bin/wvf scan http://localhost:PORT` needs a Chrome/Chromium binary;
  set `browser_path` in wvf.yml or `WVF_BROWSER_PATH` if not auto-found

## Conventions

- `cmd/wvf` is a thin main; all code under `internal/`
- context.Context everywhere work can block; whole-scan deadline
- See `analysis.md` (design) and `build-plan.md` (phases)
