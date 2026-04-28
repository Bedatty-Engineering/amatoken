# amatoken

<img src="assets/img/amatoken-logo.png" alt="amatoken" width="120" align="left"/>

Self-hosted observability for **Claude Code** usage. Reads `~/.claude/projects/**/*.jsonl`,
aggregates tokens and cost by session / project / model, and serves a single-binary
dashboard. Pricing is pulled from **OpenRouter** automatically — no manual price
upkeep required.

<br clear="all"/>

---

## Highlights

- **Dashboard** with cost / sessions / messages / tokens cards, all with **period-over-period delta** (▲ red / ▼ green) — works even on `All time` (falls back to month-over-month).
- **Stacked daily/hourly chart** with rich hover tooltips (per-bucket cost, token breakdown, share of period). Bars are clickable → modal with per-model breakdown for that day/hour.
- **Top 15 projects** and **top 15 models by spend**, ranked side-by-side on the dashboard. Clicking a row applies the filter without leaving the page.
- **Sessions tab** — paginated table with free-text search across project, branch, model, session id; click any row for a **drill-down modal** showing every assistant message with its individual cost.
- **Multiple named budgets** (calendar-month). Up to 5 can be pinned to the dashboard banner. Per-row Save/Delete with inline ✓ / ✗ feedback.
- **OpenRouter pricing engine** — Anthropic-only models, periodic auto-sync (toggleable) + manual sync from UI. Manual edits to a row preserve its source so the next sync still pulls upstream values; rows you create from scratch stay manual forever.
- **Auto-refresh** toggle (re-ingests every 60s) next to the manual `Refresh now` button.
- Confirmation modal on destructive actions, branch column normalises empty/`HEAD` to `— no branch —`, USD inputs prefixed with `$`.

---

## Prerequisites

| Requirement | Minimum | Notes |
|---|---|---|
| Docker Engine | 20.10+ | `docker --version` |
| Docker Compose v2 (optional) | 2.x | `docker compose version`. Without it use the plain `docker run` flow below. |
| Claude Code | recent build | the app reads from `~/.claude/projects/`; you need at least one logged session. |
| OS | Linux or macOS | Windows: run inside WSL2. |
| Free port | 8080 | change in `docker-compose.yml` or via `-p` if it conflicts. |

**Permissions you need to know:**

- `~/.claude/projects` is usually mode `700` (only your user can read).  
  → The container must run as your UID/GID. The `docker-compose.yml` sets
  `user: "${UID}:${GID}"` and the `docker run` example below uses
  `--user "$(id -u):$(id -g)"`.
- The named volume `amatoken-db` holds the SQLite file. Wiping it loses budgets,
  manual prices, settings — but the JSONL re-ingestion runs automatically on
  the next start.

---

## Quick start (Docker Compose)

```bash
cd amatoken
export UID=$(id -u) GID=$(id -g)        # so compose's user: gets your real ids
docker compose up --build -d
xdg-open http://localhost:8080          # or: open / your browser
```

Day-to-day commands:

```bash
docker compose logs -f                  # follow logs
docker compose restart                  # restart in place
docker compose down                     # stop, keep volume
docker compose down -v                  # stop and wipe DB
docker compose up --build -d            # rebuild after code changes
```

---

## Quick start (plain Docker)

If the Compose v2 plugin isn't installed:

```bash
cd amatoken
docker build -t amatoken .
docker volume create amatoken-db

docker run -d --name amatoken \
  --user "$(id -u):$(id -g)" \
  -p 8080:8080 \
  -v "$HOME/.claude/projects:/claude-projects:ro" \
  -v amatoken-db:/data \
  --restart unless-stopped \
  amatoken
```

Day-to-day:

```bash
docker logs -f amatoken                 # logs
docker restart amatoken
docker rm -f amatoken                   # stop & remove (volume preserved)
docker volume rm amatoken-db            # wipe history
```

---

## Verifying it works

```bash
curl localhost:8080/healthz             # → ok
curl localhost:8080/api/summary | jq    # totals + cost
curl localhost:8080/api/pricing/status  # last sync, provider, errors
```

Open `http://localhost:8080`. First load shows historical sessions immediately
(initial scan blocks startup briefly, then HTTP serves while ingestion finishes
in the background). New Claude Code sessions appear within 60s (reconcile tick)
or instantly via `fsnotify`.

---

## Configuration

Environment variables (sensible defaults):

| Variable | Default | Purpose |
|---|---|---|
| `CLAUDE_PROJECTS_DIR` | `/claude-projects` | Where the JSONL files live inside the container (set by the volume mount). |
| `DB_PATH` | `/data/amatoken.db` | SQLite file path. |
| `LISTEN_ADDR` | `:8080` | HTTP bind address. |
| `RECONCILE_INTERVAL` | `60s` | Periodic full re-scan in case fsnotify missed an event. |
| `PRICING_SYNC_INTERVAL` | `12h` | OpenRouter auto-sync cadence (only runs while the toggle is on). |

In-app settings (persisted in SQLite, editable from the UI):

| Setting | Default | Lives in |
|---|---|---|
| `pricing_auto_sync` | `true` | Toggle in the **Pricing** tab. |
| `auto_refresh_enabled` | `false` | Toggle next to **Refresh now** in the header. |

---

## API

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness. |
| GET | `/api/summary?from=&to=&project=&model=` | Tokens + cost USD totals + per-model breakdown. |
| GET | `/api/timeseries?bucket=day\|hour&...` | Time series — tokens AND cost per bucket. |
| GET | `/api/sessions?limit=&offset=&q=&...` | Paginated session list with free-text search. |
| GET | `/api/sessions/{id}/records` | Drill-down: per-message records for one session. |
| GET | `/api/rankings/projects?...` | Per-`cwd` cost / sessions / messages, sorted desc. |
| GET | `/api/rankings/models?...` | Per-model cost, sorted desc. |
| GET | `/api/filters` | Distinct project keys (`cwd` first, falls back to slug) and models for the UI selects. |
| DELETE | `/api/records/{id}` | Remove a single ingested record. |
| GET / POST / PUT / DELETE | `/api/pricing` | CRUD for the model pricing table. |
| POST | `/api/pricing/sync` | Force OpenRouter sync now. |
| GET | `/api/pricing/status` | Last sync time, provider, errors, row count. |
| GET / POST / PUT / DELETE | `/api/budgets` | CRUD for budgets. |
| GET / PUT | `/api/settings` | Key/value app settings (auto-sync, auto-refresh, …). |
| POST | `/api/ingest/refresh` | Force a full reconcile of `CLAUDE_PROJECTS_DIR`. |

---

## How ingestion works

- Only lines with `type == "assistant"` and a `message.usage` block become rows. `type=user`, `tool_result`, etc. are ignored — they only contribute to the `input_tokens` of the **next** assistant message.
- Synthetic events (`model == "<synthetic>"`) — context compactions, system prompts — are excluded from every aggregation. They have no real cost.
- Dedup is by `message.id` (`INSERT OR IGNORE`).
- Per-file byte offset is stored in `ingest_state`; container restarts don't re-ingest.
- `fsnotify` watches every subdir of `CLAUDE_PROJECTS_DIR` (with 500ms debounce). The reconcile tick (default 60s) catches events the watcher missed.

### Project identity = `cwd`, not slug

Claude Code names project directories after the cwd in which a session **started**, but the cwd inside the JSONL can change as you `cd` around mid-session. amatoken groups by the per-record `cwd` (falling back to project_slug when cwd is missing) so subprojects under the same starting directory show up as distinct rows in the rankings.

---

## Pricing engine

Implemented as a clean **provider/registry/calculator** trio:

```
internal/pricing/
├── provider.go     # Provider interface { Name(), Fetch(ctx) → []ModelPrice }
├── openrouter.go   # OpenRouter implementation (anthropic/* models only)
├── registry.go     # Coordinator: Sync(), Run(periodic), Status()
├── rates.go        # SeedDefaults — offline fallback when OpenRouter is unreachable
└── calc.go         # Calculator (CostEngine) with progressive model-id matching
```

Three source levels with strict priority:

| `source` | Origin | Sync behaviour |
|---|---|---|
| `manual` | New row created in the UI | **Never** overwritten by sync. |
| `openrouter` | Pulled from OpenRouter (incl. rows you've edited that came from OpenRouter) | **Always** refreshed on every sync — your edits get reset to upstream values intentionally. |
| `seed` | First-run offline fallback | Replaced as soon as OpenRouter sync succeeds once. |

Model-id matching has fallbacks: exact match → strip `-YYYYMMDD` date suffix → walk up `-N` version segments. So `claude-haiku-4-5-20251001` resolves to `claude-haiku-4-5`, `claude-opus-4-7` resolves to `claude-opus-4` if no specific entry exists. OpenRouter's `claude-opus-4.7` is auto-normalised to `claude-opus-4-7`.

---

## Project layout

```
amatoken/
├── cmd/server/main.go          # entrypoint, wiring, graceful shutdown
├── internal/
│   ├── ingest/                 # parser, scanner, fsnotify watcher
│   ├── storage/                # SQLite open + migrations + repo (queries)
│   ├── pricing/                # Provider, OpenRouter, Registry, Calculator
│   ├── seed/                   # First-run example budget + manual pricing
│   └── httpapi/                # chi router, handlers, embedded static UI
│       └── static/             # index.html + app.js (Alpine) + styles.css + Chart.js via CDN
├── assets/img/                 # logo, copied into static/ at build time
├── Dockerfile                  # multi-stage: golang:1.23-alpine → alpine:3.20
├── docker-compose.yml
├── go.mod / go.sum
└── README.md
```

Stack: **Go 1.23**, **chi** (router), **modernc.org/sqlite** (pure Go, no CGO), **fsnotify**. Frontend: vanilla **Alpine.js** + **Chart.js** via CDN, served as `go:embed` static files. Final image is ~21 MB (Alpine + statically linked binary).

---

## Tips on lowering your spend

amatoken is the *measurement* tool — but here's what tends to move the needle:

- **Switch model per task.** Haiku ($1/$5/M) for greps and renames, Sonnet ($3/$15/M) for most coding work, Opus ($5/$25/M) for architecture and tough debugging. The **Top models by spend** panel makes it obvious which model is eating your budget.
- **Start a fresh session for unrelated work.** As context grows, occasional cache writes (priced ~1.25× input) add up. New session = clean cache.
- **Be specific.** Vague prompts trigger exploration; precise file/line references skip it.
- **`Read` with `offset`/`limit`** instead of letting Claude pull whole large files.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `open db: unable to open database file` | Container UID can't write to `/data`. | Use `--user "$(id -u):$(id -g)"` (already in compose). |
| Dashboard empty despite JSONL existing | Container can't read `~/.claude/projects` (mode 700). | Same fix: run as your host UID. |
| `port is already allocated` | Port 8080 taken. | Re-map: `-p 9090:8080` (or change in `docker-compose.yml`). |
| `docker compose up --build -d` → `unknown flag: --build` | Compose v2 plugin missing. | `sudo apt install docker-compose-v2` (Ubuntu/Debian); or use the plain `docker run` flow above. |
| New session not appearing | fsnotify missed the create. | Click **Refresh now**, wait up to 60s, or `curl -X POST localhost:8080/api/ingest/refresh`. |
| A model shows `$0.00` cost | No pricing row for that exact id, and no fallback matched. | Click **Sync from OpenRouter**, or add the row manually in the **Pricing** tab. |
| OpenRouter sync fails | Rate limit / network blip. | Cached values keep working; the next periodic tick retries. Check `GET /api/pricing/status`. |

---

## Development

No Go toolchain needed locally — the build runs inside `golang:1.23-alpine`:

```bash
docker compose up --build -d            # rebuild + restart
docker compose logs -f
```

If you do have **Go 1.23+** installed and want hot-iterate:

```bash
CLAUDE_PROJECTS_DIR=$HOME/.claude/projects \
DB_PATH=./amatoken.db \
  go run ./cmd/server
```

Then visit `http://localhost:8080`. Static assets (HTML/JS/CSS) and migrations are embedded via `go:embed` — `go run` always reflects the current source.

---

## License

© 2026 — All rights reserved. Self-hosted Claude usage monitor.
