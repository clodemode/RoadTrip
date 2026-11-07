<!-- CLODE-COLD-START-GATE v3 — GENERATED from ABOUT/CLODE/GATE/cold_start_gate.py; edit the source and run `python3 ABOUT/CLODE/GATE/cold_start_gate.py bake`, never this block; the SessionStart hook injects this same ritual at turn 0 -->
> ### ⛔ COLD START — orient before your first Edit, grep, or subagent
>
> This is a **CLODE platform thread.** Inline work and a bare `Agent(subagent_type=…)` carry **none** of the contract — no LORE guidance, no DOMINO-equipped MCP tools, no HEY_LEAN presence — and are **invisible to sibling threads**, so they collide silently. Do these two **first**:
>
> 1. **`agent_start(session=…, scope=…, cwd=…, branch=…, stale_seconds=900, app_scope='roadtrip', lair=…, tag=…)`**
>    **ONE call** — registers presence on HEY_LEAN, snapshots live siblings, and returns the scoped briefing. Compose **every** briefing dimension you know (AND semantics; a bare `lair=` ranks 20 slots against 800+ rows — composed, against dozens). In this repo that means **`app_scope='roadtrip'`** plus the `lair=` you are working in. Ruled 2026-08-11: precision over capacity, cap stays 20.
>    Read **all three sections**. A sibling on your surface → freeze a HEY_LEAN baseline before touching it. Each section carries its own `status`: a briefing error does **not** mean the beacon went unwritten.
>    *Fallback, equally contract-satisfying, if `agent_start` is unavailable or a section errors:* `agent_register(...)` → `agent_active(stale_seconds=900)` → `ido_briefing(app_scope='roadtrip', lair=…)`.
> 2. **Dispatch the work as a CODI** — not inline, not a bare subagent. A dispatched CODI opens its own beacon with `agent_codi_start(...)` (RULED 2026-09-03: the band is in the NAME — `agent_start`/`agent_codi_start`, `agent_heartbeat`/`agent_codi_heartbeat`, `agent_stop`/`agent_codi_stop`). Out-of-scope finds → **`lairs_lore_add(lore_type='todo')`**, *never* a `spawn_task`/chip. Every dispatch prompt — and your own thread-end — must carry the completion line: *On completion call ``codi_ledger_record(codi_slug=…, status=…, lore_refs=<every slug you consulted or obeyed, INCLUDING rows seen only as a one-line `dont` in a briefing>)``, or pass `no_lore_consulted=True` if genuinely none.*
>
> About to call Edit/Write/Task before doing 1–2? **Stop and orient.**
<!-- /CLODE-COLD-START-GATE -->

# CLAUDE.md — RoadTrip

A live surface for one road trip. **Go static binary + SQLite**, deployed to **Fly** as `roadtrip-clodemode`, live at **https://roadtrip.opeongo.net**. Provided by Austin Voice Data LLC.

[README.md](README.md) is the authority — it is a long-form design record, not a summary, and every configuration table below is stated there in full. [AGENTS.md](AGENTS.md) carries the agent contract. This file is only the short list of things that bite.

---

## Critical rules (bite immediately)

1. **There is no `manage.py` and no container in the ABOUT fleet.** roadtrip is not a Django TARGET_APP and has no compose service here. Build and test at the repo root:
   ```bash
   go build ./... && go vet ./... && go test -race -count=1 ./...
   ```
2. **`app_scope` is `roadtrip`** (ruled 2026-09-08). LORE homes in `roadtrip/LORE/<character>/<slug>.yaml`. Rows still reading `app_scope='bloodride'` are pre-ruling history.
3. **Nothing private goes in the repo or the seed.** `$DATA_DIR/private.json` on the volume holds addresses and notes, keyed by ride and then by checkpoint ord (the top-level block is the first ride's), rendered only to the signed-in trip-keeper. Read one ride's entries with `privateData.forRide`, never the map directly. `git check-ignore` it before you commit anything near it.
4. **Never render a coordinate that did not come out of the store.** Fuzzed checkpoints are snapped to 0.05° on the way out; a direct read bypasses the only place that protection exists.
5. **Timezones are per-event, never per-ride.** Store UTC plus the IANA zone. A ride-wide zone misprints every stamp after the first state line.
6. **`Deploy` is `workflow_dispatch` only, and `plan` is the default.** Never add a push trigger — the site is live under someone's thumb from a car.
7. **The repo is PUBLIC** (`clodemode/RoadTrip`, CamelCaps = public under `naming-clodemode-repo-case-signals-visibility`). Do not treat it as publishable without an explicit ruling.
8. **A new trip is never added to `seed/`.** It is a file on the volume, `$DATA_DIR/trips/<slug>.geojson`, pushed with `POST /driver/trip` and shown once `POST /driver/ride/active` names it. Fixtures are synthetic and live in `testdata/`; `guard_test.go` fails on a trip document anywhere else, and on a missing `data/` or planning-handoff line in `.gitignore` / `.dockerignore`.
9. **`RIDE_SLUG` is only the fallback.** The ride the site shows is `App.activeSlug()`, read from the `meta` table per request. Never read `a.rideSlug` to decide what to render.
10. **A plan change never reaches an arrived checkpoint, a post or a photo.** `Store.planConflicts` refuses it whole. Do not add a path that re-plans a trip with options around `Store.ApplyPlan`.

11. **A checklist's private text is read in one place.** `Store.Checklist(slug, withPrivate)` does not select the private columns unless asked, and the page asks only for a signed-in request. `/checklist.json` never asks. Do not add a read of `checklist_items.private` or `private_note` beside it, and do not log a note or a refused value: a refusal names the field.
12. **A checklist document is never in the repo.** It is `$DATA_DIR/trips/<slug>.checklist.json`, pushed with `POST /driver/checklist`. `guard_test.go` fails on a `*.checklist.json`, or anything shaped like one, outside a synthetic fixture in `testdata/`. A re-push never writes a done step or an answered question (`Store.ApplyChecklist`).

13. **Chester spends money and talks to a third party.** Every Chester route is behind `requireAuth`; keep it that way. The key is `ANTHROPIC_API_KEY`, a Fly secret the operator sets: it is read once into `chesterConfig`, never logged, rendered or committed, and `guard_test.go` fails on anything shaped like a key in the tree. What the API is told is built in one place, `App.chesterContext`, and its one coordinate comes out of `publishPosition`; do not add the private overlay, a checklist's private text or a raw point to it. The model is a setting per mode (`CHESTER_MODEL_QUICK`, `CHESTER_MODEL_PLAN`), never a constant in a request. Change the prompt, the context block or the tool and `chesterPromptVersion` changes with it.
14. **No test, and no agent, calls the real API.** Chester's tests stub the Messages API at the SDK's HTTP boundary (`apiStub` in `chester_test.go`). A live question is asked only with the operator's key on a running instance, and it costs money.

## Layout

| Path | What |
|---|---|
| `main.go`, `store.go`, `handlers.go`, `auth.go`, `photo.go`, `private.go` | the whole app — Go sources at the repo root, no code subdirectory |
| `trip.go`, `plan_handlers.go`, `convert.go` | trips on the volume and the planner: the trip file and its options, `/plan`, `/driver/trip`, `/driver/plan`, `/driver/ride/active`, and the `-convert` flag |
| `checklist.go`, `checklist_handlers.go` | a trip's checklist: the document and its checks, the store seam for private text, `/checklist`, `/checklist.json`, `/driver/checklist`, `/driver/checklist.json`, and the check-off at `/driver/checklist/{step\|question}/{id}` |
| `chester.go`, `chester_handlers.go` | Chester, the en-route helper: settings, the price table, what the API is told, the place cards, the one request, the log and its summary; `/chester`, `/chester/ask`, `/driver/chester/log`, `/driver/chester/log.json`, `/driver/chester/log/{id}` |
| `freelance.go` | FreeLance: Destined on `/driver`: per-user mode and destination in memory (never the plan), TomTom search/route/along-route, the HUD view and "Find picks" with one Chester call logged as mode `picks`; `POST /driver/freelance/{mode,dest,picks}` |
| `seed/*.geojson` | RWGPS-import shape: one `LineString` per leg; `Point` features `kind=checkpoint` (each with its own `tz`) and `kind=candidate`. The embedded history; nothing is added here |
| `testdata/` | synthetic fixtures (invented places), each marked `"synthetic": true` |
| `APP_PROFILE.yaml` | the venue paths: binary `/app/roadtrip`, data `/data`. The CLODE harness reads it |
| `templates/`, `static/` | server-rendered pages and assets |
| `data/` | local volume stand-in — **gitignored**, holds `roadtrip.sqlite3`, `media/`, `private.json`, `trips/` |
| `LORE/` | YAML LORE, canonical; `lore_yaml_load` materializes it into DB_INTENT |
| `.github/workflows/` | `ci.yml` (push/PR), `deploy.yml` (dispatch only), `data.yml` (by hand, writes `private.json` only) |

## Running locally

```bash
docker build -t roadtrip .
docker run -p 8080:8080 --env-file .env -v "$PWD/data:/data" roadtrip   # .env from .env.example; gitignored
```

First boot seeds from `seed/*.geojson`; after that the volume is the source of truth and a redeploy never clobbers rows edited through the UI. With no users file and no `ADMIN_PASSWORD_HASH` (which only seeds user `kris` once) the site is read-only; users live in `$DATA_DIR/users.json` (`users.go`) and are managed at `/driver/users`. Generate one with `docker run --rm roadtrip -hash 'your-password'`. Passwords are 6+ characters, length the only rule. Reset one with `fly ssh console -a roadtrip-clodemode -C "/app/roadtrip -set-password kris"` (or open a shell with plain `fly ssh console -a roadtrip-clodemode`: the image carries a static busybox `sh`); the running server re-reads `users.json` when it changes, so no restart.

The full environment table (`SITE_URL`, `SITE_VISIBILITY`, `PIN_PRECISION`, `RIDE_SLUG`, `PUSH_TOKEN`, `DATA_DIR`, `GOMEMLIMIT`, …) is in [README.md](README.md#configuration) — read it there rather than restating it.

## Verifying a change

Deterministic first, browser second:

1. `go test -race -count=1 ./...` — the fixtures seed a throwaway SQLite from the embedded geojson. No service, no container, no network. With no Go toolchain on the host, run it in Docker:
   ```bash
   docker run --rm -v "$PWD":/src -v roadtrip-gomod:/go/pkg/mod -w /src golang:1.25 \
     sh -c 'test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race -count=1 ./...'
   ```
2. Against a live instance, `GET /driver/posts.json` is the census — **every** post with its photo identity, signed-in only. `/route.geojson` (signed-in only) drops unpinned posts, so it cannot stand in for it. `GET /driver/rides.json` lists every ride with its row counts and which one is active; `GET /plan.json` is the active plan's days and totals. `GET /driver/checklist.json` is the checklist with its private text; `/checklist.json` is the same without private text (both signed-in only since 2026-11-07). `GET /driver/chester/log.json` is every question Chester was asked, with its cost and its verdict.
3. `/healthz` answers on any host, even while `SITE_VISIBILITY=private` closes the rest of the site.

## SessionStart hook

`.claude/settings.json` wires `.claude/hooks/session-start.sh`, which warms the Go module and build caches — for the web sandbox only (it exits immediately unless `CLAUDE_CODE_REMOTE=true`). This is roadtrip's own hook and is **not** owned by the CLODE cold-start gate generator; the gate registers only this file and `AGENTS.md`.
