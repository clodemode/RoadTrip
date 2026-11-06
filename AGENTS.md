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

# RoadTrip — Agent Rules

RoadTrip is a live surface for one road trip: planned legs and checkpoints from a seed file, plus posts and photos from the phone as the miles go by. First ride: **Austin → San Jose**, 2026-09-04 to 09-07. A single **Go static binary + SQLite**, deployed to **Fly**, live at **https://roadtrip.opeongo.net**.

The masthead creature is **Odo**, a nine-banded armadillo — *Odo counts every mile.*

## Context Scope

Use only knowledge from this project. Do not rely on conventions or patterns from other codebases or training data unless they align with this project's explicit rules.

## Orient first

Read, in order: [README.md](README.md) — it is the long-form design record and the authority on every table below — then [CLAUDE.md](CLAUDE.md) for what bites, then `LORE/` (YAML, materialized into DB_INTENT by `lore_yaml_load`).

**`app_scope` is `roadtrip`.** Ruled 2026-09-08 (`spec-clode-roadtrip-becomes-its-own-app-scope-bloodride-retires-to-static-2026-09-08`). Earlier rows were filed under `bloodride` because roadtrip had no scope of its own; that is history, not a pattern to copy. File new rows with `app_scope='roadtrip'` and they home in `roadtrip/LORE/<character>/<slug>.yaml`.

## The hard constraints

1. **Not Django, and not in the ABOUT compose.** No `manage.py`, no container in the fleet. `go build ./...`, `go vet ./...`, `go test -race ./...` at the repo root. The repo IS mounted at `/app/fleet/roadtrip` in `about-intent-1` — for its LORE home and commit corroboration only, never to run it.
2. **Private text never enters the repo or the seed.** Street addresses and host notes live in `$DATA_DIR/private.json` on the volume, keyed by ride and then by checkpoint ord, rendered on signed-in surfaces only; absent file = no private data (`pattern-go-microvm-private-overlay-json-on-data-volume-never-in-repo`). A later removal commit does not help — the string stays in history.
3. **Fuzzed points are snapped on the way OUT of the store.** A checkpoint carrying `precision: "fuzzed"` is snapped to the 0.05° grid before any page, pin or GeoJSON sees it. Never bypass the store to render a raw coordinate.
4. **Every time is UTC plus the IANA zone it happened in.** There is deliberately no ride-wide timezone — the trip crosses CDT → MDT → MST (no DST in Arizona) → PDT, and one zone misprints every stamp after the first border.
5. **Planning data is seed-owned until actualized.** Boot refreshes legs, unstamped checkpoints and candidate stops from `seed/*.geojson`. Arrived checkpoints, posts and photos are never touched. A seed edit reaches the site on the next deploy.
6. **Media is content-addressed and the store is append-only.** Replacing a photo is a delete and a re-post, never an edit in place.
7. **`PUSH_TOKEN` is the same gate, entered by machine.** It is checked wherever the session is, with `subtle.ConstantTimeCompare`; unset disables the header path entirely rather than matching an empty string. Never log the value — boot logs `push token: set` / `unset`.
8. **A trip lives on the volume, not in the repo.** A new trip is a document at `$DATA_DIR/trips/<slug>.geojson`, pushed with `POST /admin/trip`; it is never added to `seed/`, and fixtures are synthetic. The ride the site shows is the active ride (`meta`, set by `POST /admin/ride/active`), with `RIDE_SLUG` only the fallback. A plan change — a saved selection or a re-pushed document — is refused whole if it would reach an arrived checkpoint or anything that holds posts.
9. **A checklist is public; its private text is not.** A trip's checklist lives at `$DATA_DIR/trips/<slug>.checklist.json`, pushed with `POST /admin/checklist`, and never in the repo. Its `private` fields and the private note taken at check-off are read only for a signed-in request, through `Store.Checklist(slug, withPrivate)`; no anonymous page and no open endpoint carries them. A public field or note holding a VIN-shaped token is refused by field name, never echoed or logged. A re-push never writes a done step or an answered question.
10. **Paths are declared in [`APP_PROFILE.yaml`](APP_PROFILE.yaml).** Binary `/app/roadtrip`, data `/data`. That file is the source of truth for them; change a path there in the same commit that moves it.

## Shipping

- **`CI`** (`.github/workflows/ci.yml`) — every push and PR: `gofmt`, `go vet`, `go build`, `go test -race`, plus a build of the production image. No secrets, cannot reach the live ride.
- **`Deploy`** (`.github/workflows/deploy.yml`) — **`workflow_dispatch` only**, deliberately no push trigger. `mode=plan` (default) runs every gate and builds without releasing; `mode=apply` releases and then independently probes `/healthz`. A deploy's own exit code is not a state report.
- **`Data`** (`.github/workflows/data.yml`) — by hand only, and it writes `private.json` and nothing else. Prod is the source of record for posts, photos and arrival stamps; pushing those upward would destroy the only copy.

```bash
gh workflow run Deploy -R clodemode/RoadTrip --ref trunk -f mode=plan
gh workflow run Deploy -R clodemode/RoadTrip --ref trunk -f mode=apply
```

## Conventions

- **Verify against the live instance, not a fixture.** `/admin/posts.json` is the census; `/route.geojson` is open data and drops unpinned posts, so it cannot answer "what is actually there".
- **`/healthz` answers on any host** — it is the one path exempt from both the canonical-host 301 and the `SITE_VISIBILITY=private` gate, because Fly's health check needs it.
- **Repo naming**: `clodemode/RoadTrip` is CamelCaps — therefore **PUBLIC** (`naming-clodemode-repo-case-signals-visibility`). Flipping it public means renaming it CamelCaps in the same pass.

## Out-of-scope finds

File them as LORE TODOs — `lairs_lore_add(lore_type='todo', app_scope='roadtrip', ...)`. Never a `spawn_task`/chip: those live outside LORE, and no `/todo` or CODI ever sees them.
