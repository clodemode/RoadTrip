# Operations

Running a RoadTrip instance: every setting, the machine credential, users, and the release workflows this repository ships with. Start with the [README](../README.md).

## Configuration

Every setting is an environment variable, read once at start. The
[README](../README.md#configuration) has the one-line version of this table;
this is the full one. "Unset" and "empty" mean the same thing. Locally the
values live in `.env` (copied from the tracked `.env.example`, ignored by git
and by the image build); on Fly the secrets are Fly secrets and the rest is
`fly.toml [env]`.

### Core

| variable | purpose |
|---|---|
| `PORT` | the port the server listens on; default `8080` |
| `DATA_DIR` | default `/data`; holds `roadtrip.sqlite3`, `users.json`, `media/`, `tmp/` (upload spill, swept at start), the optional `private.json` and the trip files in `trips/`. This directory is the whole of an instance's state: back it up |
| `ADMIN_USERNAME` | the first user's name, `admin` when unset; read only when `ADMIN_PASSWORD_HASH` creates that user |
| `ADMIN_PASSWORD_HASH` | a bcrypt hash (print one with the `-hash` flag); used once, to create the first user, an admin named by `ADMIN_USERNAME`, when `$DATA_DIR/users.json` does not exist. After that the users file is the source of truth. No users ⇒ the site is read-only |
| `SESSION_KEY` | HMAC key for session cookies; unset ⇒ random per start (everyone signs in again after a restart) |
| `PUSH_TOKEN` | fixed bearer token accepted wherever the session is; unset ⇒ the header path does not exist. See [Pushing with the token](#pushing-with-the-token) |
| `COOKIE_SECURE` | default `1`; set `0` only for a plain-HTTP local run, or the session cookie never comes back |
| `SITE_URL` | the canonical origin, e.g. `https://trip.example.com`; unset (the local default) ⇒ no redirect and no canonical/og tags. **The `fly.toml` in this repository sets it to the maintainers' site: change it before you deploy, or every request is redirected there** |
| `SITE_VISIBILITY` | `public` (default in code) or `private`; **private puts the whole site behind sign-in** (see [While the site is private](#while-the-site-is-private)) |
| `RIDE_SLUG` | the ride `/` shows **when no active ride has been set**; default `atx-sjc-2026-09`. The active ride is data, set by `POST /driver/ride/active` (see [Trips](TRIPS.md)) |
| `PIN_PRECISION` | `snap` (default) or `exact`; decides only whether the post form's **Fuzz This** toggle starts ticked at a private place |
| `GOMEMLIMIT` | optional; unset ⇒ the GC's soft limit is 75 % of the cgroup limit or MemTotal (logged at boot as `memory limit: …`) |

Two files on the volume are configuration too:

| file | purpose |
|---|---|
| `$DATA_DIR/trips/<slug>.geojson` | a trip that is not in the image: pushed with `POST /driver/trip`, read at boot after the embedded seeds; never in the repo. Absent = only the embedded rides |
| `$DATA_DIR/private.json` | private overlay, keyed by ride and then by checkpoint ord: the top-level `{"checkpoints":{"<ord>":{"address":"…","notes":"…"}}}` is the first ride's (`RIDE_SLUG`), every other ride has its own block under `"rides":{"<slug>":{"checkpoints":{…}}}`; rendered on admin pages only, never in the repo or the seed; absent = no private data |

With `SITE_URL` set, every page carries `<link rel="canonical">` and one
link-preview tag block (the `social_meta` partial, in the ride pages and the
player shell alike): `og:*` and `twitter:*` tags naming that origin, with
`og:image` the baked card `/static/img/og-card.png`, a 1200 by 630 PNG at an
absolute https address. With `SITE_VISIBILITY=private` the block names no
trip: a generic title and description over the same card. A request on any
other host (a `.fly.dev` host included) is answered with a 301 to the same path
on `SITE_URL`; only `/healthz` answers on any host, because a platform health
check needs it to.

The card is committed, not drawn at run time; `tools/og_card/README.md` says
how to bake it again. After a deploy that changes the card, Facebook keeps its
old copy until someone presses Scrape Again in its Sharing Debugger.

### Optional features that need an outside account

Each is off when its variable is unset, the page says so, and the rest of
the site is unaffected.

| variable | purpose |
|---|---|
| `ANTHROPIC_API_KEY` | the key Chester asks the Claude API with. **A secret: a Fly secret on the live site, the gitignored `.env` for a local run, never a tracked file**; unset ⇒ Chester is off, its page says so and boot logs `chester: disabled`. See [Chester](CHESTER.md) |
| `CHESTER_MODEL_QUICK`, `CHESTER_MODEL_PLAN` | the model each mode asks; both default `claude-opus-5-5`. It must be a model the web search tool in use (`web_search_20260209`) is documented for: `claude-opus-5-5`, `claude-opus-5`, `claude-opus-4-8`, `claude-opus-4-7`, `claude-opus-4-6`, `claude-sonnet-5-5`, `claude-sonnet-5` or `claude-sonnet-4-6`. Any other leaves Chester off, and boot logs why |
| `CHESTER_EFFORT_QUICK`, `CHESTER_EFFORT_PLAN` | `low`, `medium`, `high`, `xhigh` or `max`; default `low` for quick and `medium` for plan |
| `CHESTER_DAILY_USD` | the most Chester may spend in one day, in dollars (the day runs midnight to midnight in the zone the ride is in); default `5`. Above zero and no more than `100`: anything else (a typo such as `1e308` included) leaves Chester off, and boot logs why |
| `CHESTER_MODEL_BACKUP` | the model a request is asked again on, once, when the API answered the first model with a 5xx or 529 before any of the answer began (nothing was charged for it); default `claude-sonnet-5-5`, `off` turns it off and the old sentence is shown. It is checked like the two above. The row's note says the first model was unavailable and which model answered, its served model is the backup, and its cost is priced at the model that ran |
| `CHESTER_FALLBACK` | `default` (the default) or `off`; whether a question the API's safety classifiers decline is re-run on the model the API recommends for that case |
| `ANTHROPIC_BASE_URL` | optional; sends Chester's questions to another address. For a local stub only: the key goes wherever this points |
| `TOMTOM_API_KEY` | a TomTom developer key. With it the driver screen shows traffic incidents on the road ahead, and the free-drive mode ("FreeLance") can search for a destination and route to it. Unset ⇒ the traffic line says "unknown" and destination search is off. Weather alerts need no key: they come from the US National Weather Service and, in Canada, Environment Canada |
| `ROADTRACKS_TOKEN` | a read-only bearer token for the music library behind **Road Tracks** (album search and playback on the driver screen) and the **Listen Inn** (the public list of what played). Unset ⇒ both are off. The library must speak the API of MusicOrgan, the maintainers' own private music server; there is no public server to point at, so for a self-hoster this stays unset today |
| `ROADTRACKS_BASE` | the library's origin; **default `https://music.opeongo.net`, the maintainers' server**. Only read when the token is set |
| `ROADTRACKS_LINK_BASE` | optional; replaces `ROADTRACKS_BASE` at the front of every link handed to the phone, for a library the server reaches at an address the phone cannot open |
| `PUBLIC_LISTEN` | default on; `0`, `off`, `false` or `no` stops a signed-out visitor hearing the tracks played on the active ride. Needs `ROADTRACKS_TOKEN`. Off: `GET /tracks/{id}/audio` answers 404, `/tracks.json` says `"listen": false` and the Listen Inn panel has no Play control. The route redirects (302) to the one short-lived address the library gives for that play's track, so no audio passes through the RoadTrip server and the key never leaves it; one address may ask 30 times a minute, then 429 |

### Diagnostics and testing

The app can keep a log of its own events and errors (called MELT in the
code): what was served, player trouble reported by the phone, ETA scores.
No coordinate, typed destination or private text is ever written to it.

| variable | purpose |
|---|---|
| `MELT_FILE` | optional path of a file that keeps every record as one JSON line, written in the background and rotated to `<path>.1` past 2 MB (the repository's `fly.toml` sets `/data/melt/roadtrip.jsonl`). Read it signed in as an admin at `GET /driver/melt.json?stream=events\|errors\|all&n=200&event=<name>` (newest first, at most 1000); with no file it answers `{"sink":"off","records":[]}` |
| `MELT_REDIS_ADDR` | optional `host:port` of a Redis; the app then also publishes the records to the lists `clode:state:melt:roadtrip:{events,errors}`. Used by the maintainers' own tooling; leave it unset |
| `MELT_VENUE` | the `venue` label stamped on every record; default `kate` (the maintainers' name for a local test instance), `fly` in the repository's `fly.toml`. Any word that names your instance will do |
| `SIM_ALLOWED` | `1` allows a simulated drive on `/driver` (see [Testing the driver screen without driving](#testing-the-driver-screen-without-driving)); anything else and the route is a 404. Never set it on the live site |
| `PLAYER_SHELL` | `0` turns off the frame that keeps music playing across page changes and serves the plain pages. A fall-back if the frame misbehaves on a phone; unset is the normal player |

### Command-line flags

The binary is also three small tools. Each does its one job and exits.

| flag | what it does |
|---|---|
| `-hash '<password>'` | prints a bcrypt hash for `ADMIN_PASSWORD_HASH` |
| `-set-password <username>` | sets an existing user's password, read from standard input (one line), in `$DATA_DIR/users.json`. The running server re-reads the file, so nothing restarts |
| `-convert <route.json> -slug <slug> -start <YYYY-MM-DD>` | converts a planning tool's route document into a trip file; see [Trips](TRIPS.md#converting-a-planning-tools-route) |

## While the site is private

`SITE_VISIBILITY=private` makes every page require a signed-in session. It is
meant for the days before a trip, while the site is still being set up. There is no second credential and no Basic auth: an unauthenticated
request is redirected to `/driver/login?next=…`. Exactly five paths stay open,
and the list is load-bearing:

- `/healthz` — Fly's health check arrives with an arbitrary `Host`; redirect
  it and the machine is marked unhealthy and stops serving.
- `/robots.txt` — so a crawler that arrives while the site is closed reads the
  exclusions instead of a redirect to a login form.
- `/driver/login` (GET and POST) — or there is no way in.
- `/static/css/app.css` and `/static/img/creature.svg` — the only two files
  the login page renders with (the creature is also the favicon). Not
  `/static` wholesale.

So while this is on, link previews are blank to anyone not signed in and
`/route.geojson` is unreachable to them. Opening the site is one setting and
a restart (on Fly: one line in `fly.toml` and a deploy), never a code change. The effective mode is logged at boot beside the canonical-host
line: `site visibility: private (…)`.

## Pushing with the token

`PUSH_TOKEN` is one fixed credential (any long random string, for example
`openssl rand -hex 24`; on Fly it is a secret) that a machine carries in a header instead of holding a session:

```
Authorization: Bearer $PUSH_TOKEN
```

`Auth.IsAuthed` answers yes to it, and `IsAuthed` is the single question both
`requireAuth` and the whole-site `visibility` gate ask. So the token opens
exactly what a signed-in browser opens — every `/driver` route, and
`/route.geojson` while the site is private — and it is not a second auth path
to get wrong, because it is not a second gate. The comparison is
`subtle.ConstantTimeCompare`; an unset `PUSH_TOKEN` disables the header
entirely rather than matching an empty one. Boot logs `push token: set` or
`push token: unset`, never the value.

It exists because signing in puts the plaintext password on the wire and leaves
a cookie jar on whatever machine did it — a poor trade for one POST from a CI
runner or one curl from the roadside.

**A write that rides on the cookie must come from this site.** The session
cookie is `SameSite=Lax`, which stops another site's form but not a page on a
sibling host of the same site: a browser sends the cookie with that page's
POST. So `requireAuth` refuses, with 403, any request that is not a GET, is
signed in by the cookie, and carries an `Origin` header whose host is neither
the host the request was sent to nor `SITE_URL`'s. A request with no `Origin`
header is unchanged, and so is one carrying the token: no browser attaches
that header by itself.

```bash
H="Authorization: Bearer $PUSH_TOKEN"; S=https://your-site.example   # your own origin

# the private overlay, whole document, replaces what is on the volume
curl -s -H "$H" -H 'Content-Type: application/json' --data-binary @private.json $S/driver/private

# delete a ride nothing has happened on: its rows, its trip file and its private block.
# 409 (with the reason) for the active ride, a ride that ships with the site, or one with an
# arrived checkpoint, a post or a photo; 404 unknown slug; 400 not a slug. Answers {"status":"deleted","ride":"<slug>"}
curl -s -H "$H" -X POST $S/driver/ride/<slug>/delete

# the census, and a post deleted by id (303 = gone)
curl -s -H "$H" $S/driver/posts.json > posts.json
curl -s -o /dev/null -w '%{http_code}\n' -H "$H" -X POST $S/driver/post/<id>/delete

# made it to: adopt a lodging post's published pin, or give a point
curl -s -H "$H" -X POST -d post_id=<post id> $S/driver/checkpoint/<checkpoint id>/arrive
curl -s -H "$H" -X POST -d lat=<lat> -d lng=<lng> $S/driver/checkpoint/<checkpoint id>/arrive

# repairing a checkpoint the seed stopped owning: clear, deploy, stamp it back
curl -s -H "$H" -X POST -d clear=1 $S/driver/checkpoint/<checkpoint id>/arrive
# … deploy, so the seed retakes the now-unstamped checkpoint …
curl -s -H "$H" -X POST -d 'at=2026-09-06T20:06:47-07:00' $S/driver/checkpoint/<checkpoint id>/arrive

# what the ride now says about itself
curl -s -H "$H" $S/route.geojson | jq '.features[] | select(.properties.kind=="checkpoint")
  | {id, order, name, arrived_at, precision}'
```

## Taking a census

`GET /driver/posts.json` is every post the ride holds — pinned or not, hidden or
not — with the leg, the checkpoint, the times, the pin decision, the published
point, and the photo's `id`, `path`, `thumb`, `sha256`, `taken_at` and
dimensions.

`/route.geojson` cannot answer this: it is open data and drops any post without
a pin. The image carries only a static busybox shell (no sqlite3), so the
app itself is the only census there is.

What it is for is duplicates, which a phone on a bad hotspot makes: the same
`photo.id` or `photo.sha256` twice, or two rows whose `taken_at` are seconds
apart. Delete the loser with `POST /driver/post/<id>/delete`. The photo bytes
stay on the volume by design — media is content-addressed and another post may
reference the same file.

## People and sign-in

Each person signs in with a username and password. Users live in `$DATA_DIR/users.json` (bcrypt hashes, role `admin` or `editor`, created, disabled); the session cookie is signed and carries the username, and a disabled user's cookie stops working at once. There is **no self sign-up**: only an admin creates a user, at `/driver/users` (add with a password or generate one, shown once; reset; disable). Editors can post, edit, check off and plan; only admins manage users. `PUSH_TOKEN` still opens every `/driver` route except `/driver/users`.

Reset a password from the machine (the old one cannot be recovered):

```bash
fly ssh console -a <your-fly-app> -C "/app/roadtrip -set-password <username>"   # type the new password, one line, press Enter
```

The image carries a static busybox `sh`, so a plain `fly ssh console -a <your-fly-app>` opens a shell too; `-C` runs the one command without one. A password is six or more characters and length is the only rule. The running server re-reads `users.json` whenever the file changes, so the new password works at once and nothing is restarted.

## Testing the driver screen without driving

A simulated drive is **one position per signed-in driver, held on the server**, so every page and every device signed in as that driver sees the same car at the same instant. Nothing ticks in a tab and nothing is kept in browser storage: the position is computed at request time from the corridor, the seed, the start instant and the compression (`sim.go`, a port of the pure helpers in `static/js/sim.js`), and is only ever consumed server-side or handed to the HUD and Chester the way a phone fix is.

It exists only where `SIM_ALLOWED=1` (set it on a local test instance, never on the live site); otherwise the start route is a 404. Three routes, all signed-in:

- `POST /driver/sim/start` (form: `corridor` = `kgn-yyz`, `seed` blank for random, `x` compression default 60, `mph` cap, `rung` mile cap) then 303 to `/driver`. The "Simulated drive" block on `/driver` posts it; `/driver?sim=1|10|100|all&corridor=kgn-yyz&seed=N&x=` does too, once, and strips itself from the URL.
- `POST /driver/sim/stop` forgets it, everywhere.
- `GET /driver/sim.json` is `{running, corridor, seed, miles_done, mph, started_at}` or `{running:false}`, never the position.

While it runs, every page carries a red sticky banner, drawn by the server, `SIMULATED GPS — Kingston→Toronto corridor, seed N, 12.3 mi at 63 mph x60`, with Stop. The HUD asks the server without a position and `hud.js` does not call `navigator.geolocation` at all (an iPhone never prompts); Chester's question falls back to the simulated position, coarsened by `publishPosition` like a phone's. A browser position, when one is sent, still wins. The drive starts 0-5 mi along the corridor, re-draws its speed each simulated minute in 55-75 mph (`mph` caps it), and jitters each fix up to 30 m sideways. The same seed is the same drive. `hud_served` MELT records say `sim: true` and carry no coordinates. The old `from=`/`to=` straight-line mode is retired.

The behaviour is stated in `features/sim_position.feature` and run by godog from `go test` (`features_test.go`, `sim_steps_test.go`).

## Shipping it

This section describes the GitHub Actions workflows in `.github/workflows/`.
They are the maintainers' release path and **name their Fly app and site
address in the workflow files** (`FLY_APP` and `PROBE_URL` in `deploy.yml`,
`SITE` in `data.yml`). In a fork, change those three values first, or ignore
the workflows and release with `fly deploy` as the README shows. The `gh`
commands below use `-R clodemode/RoadTrip`; name your fork instead.

Two workflows release code, and the split between them is the point.

**`CI`** (`.github/workflows/ci.yml`) runs once per merge to trunk, as the
record: `gofmt`, the JS syntax check and JS tests, `go vet`, `go build`,
`go test` (the behaviour scenarios plain, every other test under `-race`), and a build of the production Docker image — because prod ships
the *image*, so an image that does not build is a broken deploy however green
the Go job is. The checks live in one shared definition,
`.github/actions/gates`. CI touches no secret and cannot reach the live ride.

**`Deploy`** (`.github/workflows/deploy.yml`) is `workflow_dispatch` **only**.
There is deliberately no push trigger: this site is live under someone's thumb
from a car, and auto-releasing every commit would put an unreviewed build in
front of the ride with no explicit go.

Releasing is lean: the suite is not run again on GitHub when CI already proved
the commit. Deploy asks GitHub whether `CI` has a completed, successful run on
trunk for exactly the commit being deployed. If it does, the gates are skipped
and the log names the CI run being trusted. If it does not (CI still running,
red or cancelled, a different commit, or the lookup failed) every gate runs in
the deploy, as before. The lean path is only ever taken on that positive proof.

**The release run sheet:**

1. Merge the PR.
2. Wait for the `CI` run on trunk to go green.
3. Dispatch `Deploy` with `apply`. Run `plan` first only if you want to see the
   image build without releasing.

```bash
# PLAN — the gates (or the CI record), and the image built on Fly's remote
# builder, WITHOUT releasing it. The default, so a slip of the finger does not
# reach the ride.
gh workflow run Deploy -R clodemode/RoadTrip --ref trunk -f mode=plan

# APPLY — the gates (or the CI record), the release, then an INDEPENDENT probe
# that the instance serves. A deploy's own exit code is not a state report.
gh workflow run Deploy -R clodemode/RoadTrip --ref trunk -f mode=apply
```

The probe hits `/healthz` on the `.fly.dev` host, which is the one path exempt
from both the canonical-host 301 and the `SITE_VISIBILITY=private` gate — so it
answers even while the site is closed.

The image keeps the platform path standard: the binary is `/app/roadtrip`
(`WORKDIR /app`) and everything persistent is under `/data`.
[`APP_PROFILE.yaml`](APP_PROFILE.yaml) at the repo root declares those paths
and the Fly app for the maintainers' own tooling; a self-hoster can ignore it.

**One-time setup:** a deploy token in the repo secret `FLY_API_TOKEN`.

```bash
fly tokens create deploy -a <your-fly-app>
# → Settings → Secrets and variables → Actions → New repository secret
```

### The data carriage

Code and data reach the volume by different roads, and only one of them is a
deploy. `fly deploy` replaces the image; it never touches `/data`. So the
private overlay — the addresses and notes that must not enter the repository —
needs a carriage of its own, and `.github/workflows/data.yml` is it.

**It travels in one direction and carries one file.** Prod is the source of
record for everything the ride produces: posts, photos, arrival stamps, all
authored from a phone on the road. Pushing any of that upward would destroy
the only copy. The overlay is the sole exception — operator-authored config
rather than a record, with its canonical copy in a repository secret, and a
freshly provisioned volume comes up without it. That is what makes it the one
thing that can safely ride up, and the one thing that has to.

```bash
# CENSUS — prove the credential, then READ the instance and report what is on
# the volume: post counts, posts sharing a photo, photos taken seconds apart,
# and which checkpoints have been arrived at. Writes nothing, anywhere.
gh workflow run Data -R clodemode/RoadTrip --ref trunk -f mode=census

# PLAN — the same credential proof, plus the secret parses and its keys are
# checkpoint ords. Writes nothing. The default.
gh workflow run Data -R clodemode/RoadTrip --ref trunk -f mode=plan

# APPLY — the same checks, the write, then a read-back comparing the
# checkpoint count the instance reports against what was sent.
gh workflow run Data -R clodemode/RoadTrip --ref trunk -f mode=apply
```

**The token gate runs first, and on its own.** It needs nothing but
`PUSH_TOKEN`, so a run still answers *does the credential work against the live
instance* when `PRIVATE_JSON` has never been set — which is the state a fresh
repository is in, and exactly when you want to know. An unset overlay secret is
a **warning** on `plan` (the useful half already ran) and an **error** on
`apply`, where there is genuinely nothing to push.

`census` is the read direction, and it is here for the same reason the write is:
so neither needs a laptop with the token on it. It answers what
`/route.geojson` cannot — that one is open data and drops an unpinned post,
and the image is `scratch`, so the app is the only census there is. What
reaches the run log is **ids and counts**: no titles, no photo paths, not a
single coordinate. The documents it pulls stay on the runner and die with the
job.

Two repository secrets: **`PUSH_TOKEN`** (the same value as the Fly secret of
that name) and **`PRIVATE_JSON`** (the whole overlay document, keys are
checkpoint ords). The carriage carries the token in one header and posts to
`/driver/private` behind the same `requireAuth` as every other admin route — the
same gate a person goes through, entered by machine, so there is no second auth
path to get wrong. Every mode proves the credential first with a
`GET /driver/posts.json`, which writes nothing. It does not check the repository
out: it talks to a running instance and has no use for the code.

The workflow logs **counts, never content**. The addresses are the whole point
of the overlay and never reach a run log.

### Editing lodging from the road

`/driver/lodging` is the same overlay as a form — every checkpoint's address and
notes for **the ride the site is showing**, editable from the phone. It
rewrites that ride's block and no other: an ord means nothing without its
ride, so one ride's entries are never shown against, or overwritten by,
another's (`privateData.forRide` is the only way to read one). It is the everyday path; the carriage is for
restoring a volume and for keeping a canonical copy off it. Clearing both
fields for a checkpoint removes its entry, which is how a stale address goes
away.

Both writers go through `savePrivate`, which writes a temp file alongside the
overlay and renames it. The overlay is re-read on *every* admin request, so a
half-written file would break the pages that exist to show it; a rename within
the directory is the one write a concurrent reader cannot catch mid-way.

The carriage follows the same rule. The shape it has always sent,
`{"checkpoints":{…}}`, replaces the first ride's block and leaves every other
ride's alone; `POST /driver/private/ride/<slug>` replaces that ride's block (a `?ride=` query parameter is refused: URLs are paths) and refuses the whole push, by name, when an entry matches no stop of the ride, either by number or by its `for` text (the refusal names the entry number and the `for`, never an address);
only a document that carries `"rides"` replaces the whole overlay.

⚠️ The two copies can drift. A `/driver/lodging` edit does not update
`PRIVATE_JSON`, and an `apply` replaces the first ride's block wholesale rather
than merging. After editing on the road, paste the result back into the secret.

### Working on it from Claude Code on the web

`.claude/hooks/session-start.sh` warms the Go module and build caches when a
web session starts, so `go build` / `go vet` / `go test` are ready immediately.
There is nothing else to install — the SQLite driver is pure Go and the tests
seed a throwaway database from the geojson the binary already embeds, so no
service and no network are needed at test time. The hook no-ops outside the web
sandbox (`$CLAUDE_CODE_REMOTE`), so a local checkout is untouched.

