# TODO: SwiftBar plugin — upgrade to KB-style full control surface

Analysis of `plugins/swiftbar/` in `knowledge-base-mcp-server` and how the
same pattern maps to wvf. No code changes made — this is a design note.

## What the KB plugin is

A full menu-bar control surface for a **launchd-managed** server, not just
a status icon:

| Piece | Role |
| --- | --- |
| `kb-mcp.10s.sh` | Plugin script: `launchctl print` for state/pid, `curl /health` for liveness, on-disk signals (`.reindex.run.json`, `*.kb-write.lock` + mtime staleness) for "indexing in progress" → `KB ●`/`◐` icons. Config sourced from `~/.config/kb/menubar.env` |
| `install.sh` | Refuses unless all preflight passes: SwiftBar present (can brew-install it), launchd service loaded+running, `/health` ok. Resolves config `plist > mcp_config.json > kb.config.yaml > defaults` → writes `menubar.env`. Symlinks **only** the plugin file into SwiftBar's dir — helpers stay in repo because SwiftBar executes every executable it finds in the plugin dir |
| `kb-mcp-rebuild.sh` | `npm run build` + `launchctl kickstart -k` + health check, in a Terminal window |
| `kb-mcp-reindex-status.sh` | `kb reindex status` in a Terminal window |
| `update.sh` | Pulls plugin files pinned to the highest `swiftbar/v*` GitHub tag, syntax-checks, re-runs installer |
| `kb-mcp-uninstall.sh` | Menu-invoked self-uninstall with native confirm dialog |
| `test.sh` | Full bash suite against temp dirs via env overrides (`KB_PLUGIN_DST`, `KB_MENUBAR_ENV`, `KB_SWIFTBAR_APP`, `KB_MCP_LABEL`, `KB_MCP_PORT`) |

## Current state here

`scripts/swiftbar/` already exists — a **minimal version** of the same idea:

- `wvf.30s.sh` probes `GET /api/jobs`, shows `WVF ▶N` / `⏳N` / `✓` / `✕`
- Start via Terminal (`bin/wvf serve`), stop via `pkill`, Scan-all POST, dashboard link
- Bare `install.sh` (symlink only, no preflight), `uninstall.sh`, `test.sh` (fake `python3 -m http.server` + temp dirs)

So the task is an **upgrade to the KB pattern**, not a greenfield build.

## Feature mapping: KB → wvf

| KB feature | wvf equivalent | Feasibility |
| --- | --- | --- |
| `launchctl` state/pid/start/stop/restart | **None today** — `wvf serve` is a manual foreground process. No plist in `~/Library/LaunchAgents`. Needs a launchd agent for full parity, or keep pgrep/lsof/pkill process management | **Decision point** |
| `/health` probe | No `/health` endpoint — `GET /api/jobs` is unauthenticated JSON on loopback and already works as the probe | Direct port |
| Indexing-activity icon (`◐`) | `jobs` table states (`running`/`queued` + `detail` "scan x.dk") — already used. On-disk alternative: `sqlite3 state.db` (WAL reads don't block; ships with macOS) | Direct port, arguably better than KB's lock-sniffing |
| `menubar.env` resolution | Simpler chain: `plist > wvf.yml (listen, state_db) / WVF_* env > defaults` | Direct port, easier |
| Rebuild + restart | `make build` (or `make check`) + restart instead of `npm run build` + kickstart | Direct port |
| Logs in Console | Only if launchd redirects stdout/stderr — `wvf serve` currently prints to its terminal | Comes free with the plist |
| Edit Devin MCP config | `wvf` already registered in `~/.config/devin/mcp_config.json` | Works as-is |
| Update check (`swiftbar/v*` tags) | Same pattern on `gundestrup/website-vitals-flow`; no tags cut yet — "fall back to `main`'s VERSION" path covers that | Direct port |
| Self-uninstall / preflight installer / `test.sh` | Straight port | Direct port |

## Options

### Option A — full parity (launchd-managed)

Add `~/Library/LaunchAgents/<label>.plist` running `bin/wvf serve` with
`KeepAlive` + `StandardOutPath`/`StandardErrorPath` log redirection.
Then the port is nearly 1:1:

- `launchctl print gui/<uid>/<label>` → state + pid
- `kickstart -k` / `bootout` / `bootstrap` → restart/stop/start
- Preflight "service loaded and running" works exactly like KB
- Auto-restart on crash, starts at login, real log files for Console

Suggested label: `eu.gundestrup.wvf` (matches the `eu.borg-collective.*`
convention already in `~/Library/LaunchAgents`).

### Option B — process-based (no launchd)

Keep `wvf serve` manual; plugin detects liveness via `/api/jobs` +
`lsof -ti tcp:<port>`, starts in a Terminal window, stops via SIGTERM.

- Less machinery, no plist to maintain
- Loses: clean state reporting, KeepAlive auto-restart, boot persistence, launchd-managed logs

## Proposed file layout (either option)

Keep the existing location `scripts/swiftbar/` (repo convention is
`scripts/` for tooling; KB uses `plugins/` but there's no need to move):

```text
scripts/swiftbar/
  wvf.10s.sh              # plugin script (menubar.env, icons, menu)
  wvf-rebuild.sh          # make build + restart, Terminal window
  wvf-jobs-status.sh      # `bin/wvf jobs` or /api/jobs dump, Terminal
  wvf-uninstall.sh        # menu-invoked confirmed self-uninstall
  install.sh              # preflight + resolve config + menubar.env + symlink
  uninstall.sh            # remove plugin symlink + menubar.env
  update.sh               # swiftbar/v* tag-pinned pull from GitHub
  VERSION
  test.sh                 # extended suite (temp dirs via WVF_* overrides)
  README.md               # mirror of KB's README structure
```

`menubar.env` → `~/.config/wvf/menubar.env` (parallel to `~/.config/kb/`).

## Open details

- Icon semantics: keep `WVF ▶/⏳/✓/✕` or unify to KB's `●/◐` style —
  `▶N` is arguably more informative (shows job count)
- Liveness probe: `/api/jobs` today; a dedicated `/health` endpoint would
  be cleaner but requires Go changes (optional follow-up)
- On-disk activity fallback: `sqlite3 state.db "SELECT … FROM jobs WHERE
  state IN ('running','queued')"` — state.db path resolved into
  menubar.env; useful if we want activity shown even when considering the
  HTTP layer suspect (KB's rationale). Optional — HTTP is fine since the
  plugin needs the server up for everything anyway
- Refresh interval: 10s (KB parity) vs current 30s
- MCP: wvf also serves `/mcp` over HTTP — could add a menu item linking
  to docs or the endpoint
