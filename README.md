# roadtrip

A live surface for one road trip: the planned legs and checkpoints from a
seed file, and posts with photos from the phone as the miles go by. First
ride: **Austin → San Jose**, 2026-09-04 to 09-07, one way.

**Live:** https://roadtrip.opeongo.net (the Fly host https://roadtrip-clodemode.fly.dev 301s there)
**Route data:** https://roadtrip.opeongo.net/route.geojson

## What it is

- `/` — the ride: hero with the day-of-trip badge, a map (leg polylines,
  numbered checkpoint pins, muted candidate stops, photo pins), the
  checkpoint list with arrival stamps, and the timeline newest-first.
- `/legs/<slug>` — one leg: its map, checkpoints, candidate stops, posts.
- `/photos` — the gallery, grouped by leg.
- `/route.geojson` — legs, checkpoints and pinned posts as open GeoJSON,
  CORS `*`, plus a `kind=track` line per leg for what was actually driven.
- `/plan` — the planner: the days with miles, hours and a long-day flag, the
  trip totals, and — for a trip that carries route options — each option as a
  control, with the roads not taken drawn dashed on the map. `/plan.json` is
  the same arithmetic as data. See "Trips on the volume".
- `/admin/post` — the driver's one screen: photo, kind, title, body, leg,
  checkpoint, zone, hide-location. One tap stamps a checkpoint as arrived.
- `/admin/posts.json` — the census: **every** post with its photo's identity,
  signed-in only. See "Taking a census".

The masthead creature is **Odo**, a nine-banded armadillo — the slow land
migrator that walked out of Texas and kept going, armoured like a car, his
banded shell a segmented odometer. *Odo counts every mile.* (The name is a
phonomeme: say it and you hear the odometer.)

Every time is stored as UTC **plus the IANA zone it happened in**, and
rendered local. There is deliberately no ride-wide zone: this trip crosses
CDT → MDT → MST (Arizona keeps no DST) → PDT, and one zone would misprint
every time after the first border. Picking a checkpoint on the post form
selects its zone; stamping an arrival moves the default zone with the car.

The seed is the RWGPS-import shape — one `LineString` feature per leg, in
order; `Point` features with `kind=checkpoint` (each with its own `tz`) and
`kind=candidate` for sights. The origin is public-safe: it is named
"Hudson Bend (home)", carries `precision: "fuzzed"`, and the app snaps any
fuzzed checkpoint to the 0.05° grid on the way out of the store, so no page,
pin or GeoJSON ever sees the real point. Street addresses never enter the
seed at all — they live in `$DATA_DIR/private.json` on the volume and show
only to the signed-in trip-keeper (the EagleLakeRocks dedications pattern).

Planning data is **seed-owned until actualized**: on every boot the seed
refreshes legs, any checkpoint that has not been stamped as arrived, and the
candidate-stop set. Arrived checkpoints, posts and photos are never touched.
So a seed edit reaches the site on the next deploy.

## Running it

```bash
docker build -t roadtrip .
docker run -p 8080:8080 -v "$PWD/data:/data" roadtrip
```

Then open http://localhost:8080. On first boot it seeds itself from
`seed/*.geojson` onto the volume; after that the volume is the source of
truth and a redeploy never clobbers rows edited through the UI.

To enable posting, set an admin password hash:

```bash
docker run --rm roadtrip -hash 'your-password'      # prints a bcrypt hash
```

then pass it as `ADMIN_PASSWORD_HASH`. With no hash set the site runs
read-only. For a plain-HTTP local run also pass `COOKIE_SECURE=0`, or the
session cookie will never come back.

### Configuration

| variable | purpose |
|---|---|
| `ADMIN_PASSWORD_HASH` | bcrypt hash; unset ⇒ read-only site |
| `SESSION_KEY` | HMAC key for session cookies; unset ⇒ random per boot (sessions drop on restart) |
| `PUSH_TOKEN` | fixed bearer token accepted wherever the session is; unset ⇒ the header path does not exist. See "Pushing with the token" |
| `RIDE_SLUG` | the ride `/` shows **when no active ride has been set**; default `atx-sjc-2026-09`. The active ride is data, set by `POST /admin/ride/active` — see "Trips on the volume" |
| `DATA_DIR` | default `/data`; holds `roadtrip.sqlite3`, `media/`, `tmp/` (upload spill, swept on boot), the optional `private.json` and the trip files in `trips/` |
| `$DATA_DIR/trips/<slug>.geojson` | a trip that is not in the image: pushed with `POST /admin/trip`, read at boot after the embedded seeds; never in the repo. Absent = only the embedded rides |
| `GOMEMLIMIT` | optional; unset ⇒ the GC's soft limit is 75 % of the cgroup limit or MemTotal (logged at boot as `memory limit: …`) |
| `$DATA_DIR/trips/<slug>.checklist.json` | a trip's checklist: pushed with `POST /admin/checklist`, read at boot after the trips; never in the repo. Holds private text. See "A trip's checklist" |
| `$DATA_DIR/private.json` | private overlay, keyed by ride and then by checkpoint ord: the top-level `{"checkpoints":{"<ord>":{"address":"…","notes":"…"}}}` is the first ride's (`RIDE_SLUG`), every other ride has its own block under `"rides":{"<slug>":{"checkpoints":{…}}}`; rendered on admin pages only, never in the repo or the seed; absent = no private data |
| `SITE_URL` | the canonical origin, e.g. `https://roadtrip.opeongo.net`; unset (the local default) ⇒ no redirect and no canonical/og tags |
| `SITE_VISIBILITY` | `public` (default in code) or `private`; **private puts the whole site behind the one admin password** — see below |
| `PIN_PRECISION` | `snap` (default) or `exact`; decides only whether the post form's **Fuzz location** toggle starts ticked at a private place |
| `ANTHROPIC_API_KEY` | the key Chester asks the Claude API with. **A secret, set by the operator on the host and never in a file here**; unset ⇒ Chester is off, its page says so and boot logs `chester: disabled`. See "Chester" |
| `CHESTER_MODEL_QUICK`, `CHESTER_MODEL_PLAN` | the model each mode asks; both default `claude-opus-5-5`. It must be a model the web search tool in use (`web_search_20260209`) is documented for: `claude-opus-5-5`, `claude-opus-5`, `claude-opus-4-8`, `claude-opus-4-7`, `claude-opus-4-6`, `claude-sonnet-5-5`, `claude-sonnet-5` or `claude-sonnet-4-6`. Any other leaves Chester off, and boot logs why |
| `CHESTER_EFFORT_QUICK`, `CHESTER_EFFORT_PLAN` | `low`, `medium`, `high`, `xhigh` or `max`; default `low` for quick and `medium` for plan |
| `CHESTER_DAILY_USD` | the most Chester may spend in one UTC day, in dollars; default `5`. Above zero and no more than `100`: anything else (a typo such as `1e308` included) leaves Chester off, and boot logs why |
| `CHESTER_FALLBACK` | `default` (the default) or `off`; whether a question the API's safety classifiers decline is re-run on the model the API recommends for that case |
| `ANTHROPIC_BASE_URL` | optional; sends Chester's questions to another address. For a local stub only: the key goes wherever this points |
| `PORT` | default `8080` |
| `COOKIE_SECURE` | default `1`; set `0` only for plain-HTTP local runs |

With `SITE_URL` set, every page carries `<link rel="canonical">` and `og:*`
tags naming that origin, and a request on any other host — the `.fly.dev`
host included — is answered with a 301 to the same path on `SITE_URL`;
only `/healthz` answers on any host, because Fly's health check needs it to.

### While the site is private

`SITE_VISIBILITY=private` (set in `fly.toml [env]` as of 2026-09-02, ruled by
Kris — *"we will open it later"*) makes every page require the existing admin
session. There is no second credential and no Basic auth: an unauthenticated
request is redirected to `/admin/login?next=…`. Exactly five paths stay open,
and the list is load-bearing:

- `/healthz` — Fly's health check arrives with an arbitrary `Host`; redirect
  it and the machine is marked unhealthy and stops serving.
- `/robots.txt` — so a crawler that arrives while the site is closed reads the
  exclusions instead of a redirect to a login form.
- `/admin/login` (GET and POST) — or there is no way in.
- `/static/css/app.css` and `/static/img/creature.svg` — the only two files
  the login page renders with (the creature is also the favicon). Not
  `/static` wholesale.

So while this is on, link previews are blank to anyone not signed in and
`/route.geojson` is unreachable to them — which is the point for now.
Re-opening the site is a one-line `fly.toml` change and a redeploy, never a
code change. The effective mode is logged at boot beside the canonical-host
line: `site visibility: private (…)`.

### Pushing with the token

`PUSH_TOKEN` is one fixed credential — 24 hex characters, set as a Fly secret —
that a machine carries in a header instead of holding a session:

```
Authorization: Bearer $PUSH_TOKEN
```

`Auth.IsAuthed` answers yes to it, and `IsAuthed` is the single question both
`requireAuth` and the whole-site `visibility` gate ask. So the token opens
exactly what a signed-in browser opens — every `/admin` route, and
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
H="Authorization: Bearer $PUSH_TOKEN"; S=https://roadtrip.opeongo.net

# the private overlay, whole document, replaces what is on the volume
curl -s -H "$H" -H 'Content-Type: application/json' --data-binary @private.json $S/admin/private

# the census, and a post deleted by id (303 = gone)
curl -s -H "$H" $S/admin/posts.json > posts.json
curl -s -o /dev/null -w '%{http_code}\n' -H "$H" -X POST $S/admin/post/<id>/delete

# made it to: adopt a lodging post's published pin, or give a point
curl -s -H "$H" -X POST -d post_id=<post id> $S/admin/checkpoint/<checkpoint id>/arrive
curl -s -H "$H" -X POST -d lat=<lat> -d lng=<lng> $S/admin/checkpoint/<checkpoint id>/arrive

# repairing a checkpoint the seed stopped owning: clear, deploy, stamp it back
curl -s -H "$H" -X POST -d clear=1 $S/admin/checkpoint/<checkpoint id>/arrive
# … deploy, so the seed retakes the now-unstamped checkpoint …
curl -s -H "$H" -X POST -d 'at=2026-09-06T20:06:47-07:00' $S/admin/checkpoint/<checkpoint id>/arrive

# what the ride now says about itself
curl -s -H "$H" $S/route.geojson | jq '.features[] | select(.properties.kind=="checkpoint")
  | {id, order, name, arrived_at, precision}'
```

### Taking a census

`GET /admin/posts.json` is every post the ride holds — pinned or not, hidden or
not — with the leg, the checkpoint, the times, the pin decision, the published
point, and the photo's `id`, `path`, `thumb`, `sha256`, `taken_at` and
dimensions.

`/route.geojson` cannot answer this: it is open data and drops any post without
a pin. And on a `scratch` image there is no shell, so nobody can read the
SQLite off the volume — the app itself is the only census there is.

What it is for is duplicates, which a phone on a bad hotspot makes: the same
`photo.id` or `photo.sha256` twice, or two rows whose `taken_at` are seconds
apart. Delete the loser with `POST /admin/post/<id>/delete`. The photo bytes
stay on the volume by design — media is content-addressed and another post may
reference the same file.

## Trips on the volume

The repository is public; a trip is not — where it starts and ends, and when.
So a new trip is **not** added to `seed/`. It is one GeoJSON document on the
volume, `$DATA_DIR/trips/<slug>.geojson`, and it gets there by push:

```bash
H="Authorization: Bearer $PUSH_TOKEN"; S=https://roadtrip.opeongo.net

# the whole trip document; parsed completely before a byte is written
curl -s -H "$H" -H 'Content-Type: application/geo+json' --data-binary @trip.geojson $S/admin/trip

# make it the ride the site shows (or add ?activate=1 to the push)
curl -s -H "$H" -X POST -d slug=<slug> $S/admin/ride/active
curl -s -H "$H" -X POST -d clear=1 $S/admin/ride/active     # back to RIDE_SLUG

# every ride in the database, its row counts, and which one is showing
curl -s -H "$H" $S/admin/rides.json
```

A push is applied in the same request — the file is written atomically and the
ride is created or refreshed at once — because a suspended machine never
re-runs boot, and "it loads on the next boot" would mean "on the next deploy".
Boot does read `trips/*.geojson` too, after the embedded seeds and through the
same path, so a fresh machine or a restored volume comes up whole; a file that
will not load is logged and skipped, never fatal. The slug is `[a-z0-9-]` and
becomes the file name; a slug that belongs to an embedded ride is refused.

**A leg line has to be where its leg is.** A pushed or volume-loaded document
is refused when a leg line's first or last point is more than **50 km** from
the checkpoint at that end of the leg (a leg ends at the last checkpoint filed
on it and starts where the leg before it ended; the first leg starts at its own
first checkpoint). A routed line ends at its checkpoint, so the margin is
generous on purpose: what it catches is a line written `[lat, lng]`, which at
longitudes inside ±90 passes the range check and draws on the wrong side of the
planet. The refusal names the leg and never a coordinate. A leg with no
checkpoint at either end is refused too, because nothing could place its line.
The same check runs on a saved selection. The embedded September seed passes
it (a test holds that), though boot does not run it on an embedded seed.

**The active ride** is a row in the `meta` table, read per request. Setting it
moves every page, the post form and `/route.geojson` together. The ride that
stops being shown is not touched: it stays in the database whole.

### A trip's checklist

A trip may carry a checklist: staged steps and open questions that the
trip-keeper checks off as they succeed, each with a time and a public note of
what actually happened. `GET /checklist` is public and written for the next
person who has to do the same thing; the masthead links to it and the front
page says *n of m done*. `GET /checklist.json` is the same page as data.

```bash
# the whole checklist document, for a ride that is already in the database
curl -s -H "$H" -H 'Content-Type: application/json' --data-binary @trip.checklist.json "$S/admin/checklist?ride=<slug>"

# check a step off now, or after the fact; the public note is "outcome"
curl -s -H "$H" -X POST $S/admin/checklist/step/<step-id>
curl -s -H "$H" -X POST -d at=2030-05-01T14:30 --data-urlencode 'outcome=What actually happened.' \
     --data-urlencode 'private_note=For the trip-keeper only.' $S/admin/checklist/step/<step-id>
curl -s -H "$H" -X POST -d clear=1 $S/admin/checklist/step/<step-id>        # undo: the time goes, the notes stay

# answer a question the same way
curl -s -H "$H" -X POST --data-urlencode 'outcome=The answer.' $S/admin/checklist/question/<question-id>

# everything, private text included: signed in or with the token only
curl -s -H "$H" $S/admin/checklist.json
```

The document is `title`, `intro`, `disclaimer`, `stages` (each `id`, `title`,
`tz`, `when`, `steps` of `id`, `title`, `detail`, `private`), `questions`
(`id`, `group`, `ask`, `why`, `private`) and `sources` (`label`, `url`). It is
parsed completely before anything is written, an unknown key is refused, and
it lands at `$DATA_DIR/trips/<slug>.checklist.json`. Without `?ride=` (or a
`"ride"` in the document) it belongs to the ride the site is showing.

**Public and private.** Titles, details, questions and the outcome notes are
public. `private`, and the private note taken at check-off, are for the
signed-in trip-keeper: the public read never loads them (`Store.Checklist`
with `withPrivate` false does not select those columns), so they are on no
anonymous page and in no open endpoint. A push is **refused** when a public
field holds a 17-character token shaped like a vehicle identification number,
and so is a public note; the refusal names the field and never the value, and
nothing logs it. That check is a shape, not a promise: a name or a plate in a
public field is not something it can see.

**A signed-in page is never cached.** `/checklist`, like every page, is one
address for the visitor and the trip-keeper. Every HTML response to a signed-in
request (the session cookie or the push token) is sent with
`Cache-Control: private, no-store` and `Vary: Cookie`, set once where the pages
are rendered, so no cache keeps a page that carries private text or hands it to
the next visitor. The anonymous response carries neither header.

**A re-push never reaches what is done.** An open step or question takes the
later document's text; a done step or an answered question is not written at
all, and one the later document no longer has is kept if done and dropped if
open. A time is stored as UTC plus the stage's IANA zone (a question takes the
zone the ride is in), and a wall-clock time typed after the fact is read in
that zone. The disclaimer is always shown; a document without one gets a
default.

### Chester

Chester is a helper for the road. The signed-in trip-keeper asks it about
fuel, food, a rest stop, coffee, tonight's lodging, something worth a stop,
the weather and roads ahead, or how far it is to tonight, in one tap or in
their own words, and the site asks the Claude API and streams the answer back
in large type. It is for the passenger, or for when the car is parked.

It is also an experiment. Every question is a row in a log, and the
trip-keeper rules on each answer after checking it against a navigation app, a
booking site, a map or the place itself. The log is what says how far such an
answer can be trusted.

**What it cannot do, and says so.** Chester has no live traffic and no
routing. A drive time is an estimate from the plan's own miles; turn-by-turn
stays with a navigation app. It can search the web for what is there now
(lodging, hours, weather, closures), and it is told to say when it could not
confirm something rather than guess.

**Two modes.** *Quick* is a short answer with at most three places, stopped
after 60 seconds. *Plan* is the evening question about tomorrow: longer, more
searching, up to six places, stopped after 180 seconds. Each mode has its own
model and effort setting; neither is fixed in the code.

**Every place is a card.** A place Chester names arrives as a card with its
name, kind, town and one line of why, and links out: a navigation app, a map,
and for lodging a booking search for that town and night. The links are built
by the server from the card's fields. Nothing the model writes is ever placed
on the page as markup, and a web address it gives is kept only when it is a
plain `http` or `https` address.

**Signed in only, and capped.** `/chester` and everything under it answer only
to the session or the push token, because every question spends money. One
question runs at a time, and a second is told that Chester is busy. A question
that fails leaves one sentence on the page and stays in the box.

**The daily limit.** Before each question the day's spending (UTC) is read
from the log, and the question is refused when that spending plus the most the
question could cost is above `CHESTER_DAILY_USD`. Checked only against what is
already spent, one long question could take the day well past the limit. The
most a question could cost is every request it may take (three) writing its
mode's `max_tokens` at the model's output price, plus every web search each
request is allowed:

| Mode | Worst case | At the default model ($20 per million output tokens) |
|---|---|---|
| quick | 3 × (8,000 tokens + 2 searches) | $0.54 |
| plan | 3 × (32,000 tokens + 5 searches) | $2.07 |

So with the default limit of $5 a plan question is asked until $2.93 is
spent, and a quick one until $4.46. The refusal says what is left today, what
the question could cost, and whether a quick one still fits; it lifts at
midnight UTC. What a question reads is not in the worst case, because nothing
bounds it in advance, and a typical question costs a small part of these
figures. A limit below a mode's worst case refuses every question in that
mode, and boot logs a warning saying so.

**What a question costs** is worked out from the response's own token counts
at list prices, plus web searches at $10 per 1,000. A model the price table
does not know is priced as the dearest one in it, so an unknown model can only
reach the limit sooner. When the API re-ran a declined question on another
model, the response lists every attempt, and each is counted at its own
model's price: the declined attempt is billed too. Without that list, each
price takes the dearer of the model asked and the model that answered.

**A question that is cut off.** When the answer stops before the API has said
how it ended (a dropped connection, the time limit, the browser going away),
there is no final count of what was written. The page and the log row then say
the answer was cut off, and that request is charged the most it could have
written: its mode's `max_tokens` at the output price. The row marks the cost
as an estimate. Searches such a request ran are not known and are not charged.
A request the API turned down outright, or that never left the machine, is
charged nothing. Whatever words had arrived when a question failed (cut off,
declined, or stopped at the length limit) are kept on the row as a partial,
apart from the answer: the page takes them down, and the log and its summary
never count them as an answer.

**What the API is told.** The active ride's plan as the public pages show it
(days, legs, miles, hours, checkpoint names and zones, arrival stamps), today's
leg and how many miles of it are left, the local time in the zone of the
nearest checkpoint, and one position: the phone's when the trip-keeper taps
*Send my position*, else the last pinned post. That position goes through the
store's fuzz seam first, so a point at or near a private place is replaced by
the same 0.05° cell a post's pin would get. The private overlay, a checklist's
private text and photos are never sent. The nearest checkpoint's town is
said only when that checkpoint's own name is one. The web search tool is given
the ride-day's time zone and nothing else: never a coordinate, and not the
town. The tools come first in the prompt, ahead of the plan, so a town or a
zone that moved with the car would discard the cached plan at every stop; the
town is in the question's own message instead, after the cached part.

**The key.** Chester needs an API key for the Claude API. The operator sets it
as a Fly secret named `ANTHROPIC_API_KEY`, by hand, with their own key; it is
not in this repository, not in `fly.toml`, and not in any log. Setting a secret
restarts the machine, and that restart is the only thing switching Chester on
takes. With no key the feature is absent: the
masthead has no link, `/chester` says Chester is not set up, and boot logs
`chester: disabled`. With one, boot logs `chester: enabled` and the two models.
`guard_test.go` fails the build on anything shaped like a key in the tree.

**The log.** `GET /admin/chester/log` lists every question: when and in which
zone, the mode, the question, the model and effort, milliseconds to the first
word and in all, tokens in, out and read from the cache, searches, dollars
(marked when they are an estimate), the answer or, for a question that failed,
what had been written, the places, how it stopped, and the fuzzed position
that was sent. A
verdict is one tap in place: **Right**, **Partly**, **Wrong** or
**Unverified**, with what it was checked against, a note, and an optional pair
of figures (Chester's and the reference, in minutes, dollars or miles). The
summary groups by mode and verdict and gives the count, the median time, the
dollars, and the mean absolute percent error where pairs exist.
`GET /admin/chester/log.json` is the whole log as data. Neither is reachable
without signing in, and nothing of the log is on any open page.

```bash
# ask from curl: the answer streams back one JSON object a line
curl -sN -H "$H" -d preset=fuel $S/chester/ask
curl -sN -H "$H" -d mode=plan --data-urlencode 'q=Where should we stay tomorrow night?' $S/chester/ask

# rule on question 12, and read the whole log
curl -s -H "$H" -d verdict=partly -d against=navigation -d chester_value=50 -d reference_value=40 -d unit=minutes \
     $S/admin/chester/log/12
curl -s -H "$H" $S/admin/chester/log.json
```

Chester only suggests. Nothing it says is written into the plan.

### Options

A trip document is the seed shape plus three optional top-level keys:

```json
"avg_mph": 60, "long_day_hours": 9.5,
"options": [{"id": "day2", "title": "Day 2 route", "default": "a",
             "choices": [{"id": "a", "label": "…", "detail": "…"}, {"id": "b", "label": "…"}]}]
```

A feature whose properties carry `"option"` and `"choice"` belongs to that
choice only; a feature without them is part of every plan. Two roads for one
day are two `LineString`s with the same `leg` and different choices. A choice
may add a day and a checkpoint — then the checkpoints after it are written once
per choice, with the `order` each has under that choice.

A leg may carry `"day": N` instead of a date. It is then dated from the ride's
start date, and **the start date is part of the stored selection** — so a
departure that slips a day is one saved field, not an edit to every leg. With
`avg_mph` set, a leg's hours are its miles at that speed, and a day over
`long_day_hours` is flagged.

**Once a ride exists, the saved start date wins over the document's.** The
first push of a trip takes `ride.start_date` from the document. After that the
start date in force is the saved selection's, exactly like the saved choices,
and a document pushed again with a different `ride.start_date` does **not**
move it: the start moves through `POST /admin/plan` (`start=YYYY-MM-DD`) and
nowhere else. The push still succeeds, and says so. Its answer always carries
`start_date`, the date in force, and when the document's differs it also
carries `document_start_date` and `start_date_not_applied`, a sentence saying
the document's start was not applied and where the start is moved. The boot
pass logs the same fact, without the dates.

The database holds one plan: the document under the stored selection. The
alternatives stay in the file. `/plan` shows them all; switching there is local
to the visitor's browser, and only the signed-in trip-keeper's **Save this
plan** (`POST /admin/plan`, fields `opt-<option>=<choice>` and `start=`) changes
the plan for everyone. `GET /plan.json?opt-<option>=<choice>` answers "what
would that cost" without saving anything.

**A saved change never reaches what the road has made real.** It is refused
whole — and says why — if the new plan would drop or re-route a leg that holds
posts, or drop, renumber or replace a checkpoint that is arrived or holds
posts. Moving the start date is never refused: it moves dates and nothing else.
A re-pushed document is held to the same rule.

**What a page says about a save is a fixed sentence.** After **Save this
plan** the page comes back to the line under the button: `Saved.`, or
`Not saved:` and why. The redirect carries a short code (`/plan?ok=saved`,
`/plan?error=conflict`, `/checklist?error=future`), never words, and each page
maps the codes it knows to its own sentences; a code it does not know prints
nothing. `/plan` and `/checklist` are public pages, and free text in a query
string would let anyone put a sentence of their choosing on the site with a
link. curl is unaffected: it gets the status and the full reason as JSON.

**Every coordinate on `/plan` has been through the store's fuzz seam**, the
roads not taken included: a `precision: "fuzzed"` checkpoint is its 0.05° cell,
and no line is drawn within 8 km of one — the line runs to the cell point
instead. The same mask now covers the ride pages and `/route.geojson`, which
used to hand back the un-snapped vertex where a leg met a private checkpoint.

### Converting a planning tool's route

```bash
docker run --rm -v "$PWD/in:/in:ro" -v "$PWD/data:/data" roadtrip \
  -convert /in/route.json -slug <slug> -start <YYYY-MM-DD> \
  -zones '<stop>=<Area/City>,<stop>=<Area/City>'
```

`-convert` reads a route document (schema `roadtrip-route/0.1`: stops, option
groups, routed segments as `[lat, lng]`) and writes a trip file to
`$DATA_DIR/trips/<slug>.geojson` — the local data dir, which is gitignored —
ready to push. A stop's label is derived from what the stop is (start, night,
finish); the planning tool's free-text notes are not carried. **It refuses a
stop that has no IANA zone** (`"tz"` on the stop, or `-zones`): there is no
ride-wide zone to fall back on. `-name` overrides the ride name, `-out` the
destination.

A test (`guard_test.go`) fails if any file git knows about is a trip document
outside `seed/atx-sjc-2026-09.geojson` and the synthetic fixtures in
`testdata/`, or if `.gitignore` or `.dockerignore` loses its `data/` or
planning-handoff line.

## Made it to

An arrival is not only a time. `POST /admin/checkpoint/<id>/arrive` takes an
optional location — `post_id`, meaning "adopt that post's published pin"
(the lodging photo *is* the fix), or an explicit `lat`+`lng` — and when one is
given the checkpoint stops being a plan:

- `arrived_at` is stamped, `lat`/`lng` are overwritten, and `precision`
  becomes `exact`. The seed put the checkpoint at a town centroid; arriving is
  the moment the site learns the actual motel.
- **unless the checkpoint is `fuzzed`**, which keeps its precision and is
  snapped before the write. The raw fix at a private place is never written
  down — an arrival must not become the one path that writes it.
- `clear=1` takes the stamp back and **leaves the location**. A mis-tap is a
  wrong time, not a wrong place.
- `at=<RFC3339>` stamps a time other than now. The button never sends it — a
  tap means *now*, which is the whole point of a one-tap stamp.

`at` exists for one job, and it is worth knowing why before you need it.
**`arrived_at` is what takes a checkpoint away from the seed** (see
*seed-owned until actualized*), so a checkpoint stamped before a seed
correction keeps its old name for good: the only repair is to clear the stamp,
let a deploy refresh it, and stamp it again. Without `at`, that third step
would overwrite the real arrival with the clock of the repair — losing the one
fact the stamp existed to hold. It came up on 2026-09-07, when checkpoints 4
and 5 were found still carrying names from before two separate corrections.

Four things are refused rather than stamped, each because it would go wrong
quietly: a time with no offset (it would silently mean UTC), an unparsable
one, the zero time (that is how the store spells a *cleared* stamp, so it would
un-stamp while looking like a stamp), and a time in the future — `defaultTZ`
follows the latest arrival, so a mistyped year would own the post form's zone
for the rest of the ride. A back-dated stamp says so in the log, with the time
it wrote.

Then the lines follow. In `loadRide`, so `/`, `/legs/…` and `/route.geojson`
cannot disagree: a leg whose terminal checkpoint has been stamped **ends** at
that checkpoint's published point, and the next leg **starts** there, because
the two share the boundary checkpoint. Only the endpoints move — the shape in
between is still the plan, since moving a pin is a fact and re-routing the day
is a guess.

And `/route.geojson` gains one `kind="track"` LineString per leg: the arrived
origin, that leg's pinned posts in `occurred_at` order, the arrived terminus.
It is built only for a leg that has some of that, and only when it comes to two
points or more — one fix is a pin, not a track. Checkpoints carry `id` (the
arrive route is keyed by it, and `order` is not it) and `made_it: true` once
stamped. On the map a leg with a track goes dashed and dim under it: both lines
are true, and the difference between them is the interesting part.

## The HEIC rule

**JPEG only.** There is no pure-Go HEIC decoder and this ships on `scratch`
with nothing to shell out to. On an iPhone set **Settings → Camera →
Formats → Most Compatible** before the trip, or share a photo as JPEG. A
non-JPEG upload is refused with exactly that message. Originals are stored
content-addressed under `media/YYYY/MM/<sha256-prefix>.jpg`; thumbnails are
800 px wide with the EXIF orientation applied. The photo's time comes from
EXIF `DateTimeOriginal`, read in the zone chosen on the form. The stored
original carries **no EXIF location** — see *Served originals carry no GPS*
below.

**Size limits, and why.** Uploads are capped at 25 MB and 60 megapixels —
every phone shipping today fits (an iPhone Pro's 48 MP full-resolution
export is 8064×6048, 13–15 MB); a medium-format frame or a stitched
panorama does not, and the form says so: *photo is N MP (W×H); the limit is
60 MP. The phone's HEIF→JPEG export at Most Compatible is fine, or share a
smaller copy.* The limits exist because a photo post is the one request
that can take the machine down: on 2026-09-02 a full-size JPEG held in
memory and decoded on the 256 MB VM was OOM-killed at anon-rss 158 MB, four
posts were lost, and the phone saw only a reset connection. The path is
bounded now — the upload streams to `$DATA_DIR/media/.upload-*` (multipart
spills to `$DATA_DIR/tmp` past 1 MB; both are swept on boot), the header is
checked against the megapixel cap before the one decode, the thumbnail is a
box average plus one bilinear pass (no float working buffer), and the Go
GC runs under a soft limit of 75 % of the machine's RAM. Measured in Docker
at `--memory=256m`: 12 MP peaks the process at 40 MB, 48 MP 4:2:0 at 91 MB,
48 MP 4:4:4 at 163 MB, none killed; the VM is 512 MB on Fly. Each photo
logs one line (`photo <sha>: W×H MP MB, orientation, thumb`), and each post
logs its pin decision (`post N: pin browser ±12 m → snapped to checkpoint 1
(Hudson Bend (home))`, `pin exif → fuzzed to checkpoint 1 (Hudson Bend
(home))`, `pin exif (exact, fuzz off) at checkpoint 1 (…)`, `pin checkpoint 3
(…) (fallback)`, `pin none (hidden)`) — names, never coordinates — so a driveway GPS test is
provable from `flyctl logs` even though the public map hides a home fix.

## Served originals carry no GPS

`/media/` is public, immutable and cached forever, so the JPEG bytes are the
one privacy decision that cannot be taken back: a pin can be fuzzed, re-pinned
or hidden after the fact, but a file that has been crawled with a GPS IFD in it
is published for good. So **every original this app stores goes through a JPEG
segment filter first** (`exif_strip.go`) — on upload, and once per volume over
everything already there.

**What survives**: `Orientation` (0x0112), because browsers rotate the original
by it and the original is never rotated on the server, and `DateTimeOriginal`
(0x9003), because the photo's time is read from it. Nothing else. The APP1 is
**rebuilt** from those two values rather than having the GPS pointer deleted —
deleting an entry means rewriting every absolute TIFF offset after it, and
merely zeroing the pointer would leave the latitude sitting in the file as
unreferenced bytes, which is exactly what must not be published. A rebuild
cannot leak by omission: GPS, MakerNote (which carries a camera serial number),
lens, software, the EXIF thumbnail and an XMP APP1 (which carries its own copy
of the coordinates) all go, whether or not the code knows the tag exists. The
rebuild is deterministic, which makes the filter idempotent — running it twice
is a no-op.

**What is not touched**: the pixels. This is segment surgery on the JPEG
stream, not a re-encode — SOF/DQT/DHT/SOS and the entropy-coded scan are
copied through byte for byte, and other application segments (APP0 JFIF, APP2
ICC) pass through unchanged. There is no quality loss and no decode, which
matters on a 512 MB machine. **The photo row's own `lat`/`lng` is deliberately
left alone**: the database keeps the exact point so a post can still be
re-pinned from it; only the served bytes lose it.

The upload's SHA — which names the file — is taken from the bytes as
uploaded, before the strip, so re-sharing the same photo is still deduplicated
to the same path.

**The one-shot sweep.** Originals stored before this existed are rewritten in
place at boot: temp file beside the original, fsync, rename over. It is keyed
in the `meta` table as `media_gps_stripped_v1` and logs one line —
`media_gps_stripped_v1: 21 original(s) — 19 rewritten without EXIF location, 2
already clean, 0 missing, 0 error(s)`. A file that is already clean is not
written at all, so it keeps its bytes and its mtime. The key is recorded only
on a clean pass, so a boot that hit an error retries on the next one, and the
sweep is not fatal — it never blocks a boot. Thumbnails are not swept: they
are re-encoded with `image/jpeg`, whose encoder writes a JFIF APP0 and nothing
else, so they have never carried an EXIF block.

**`robots.txt`** answers at `/robots.txt` and is **open even while the site is
private**, so a crawler that arrives early reads the exclusions instead of a
302 to the login form:

```
User-agent: *
Disallow: /admin/
Disallow: /media/
Allow: /
```

`/media/` is excluded because an original is a photo, not a document: it
should not turn up in an image search, and a file that has been indexed cannot
be un-indexed on the day someone changes their mind.

## Where a pin comes from

A photo taken **through the form** on an iPhone arrives with no GPS and no
`DateTimeOriginal`: Safari strips every EXIF tag but orientation from a web
camera capture (measured 2026-09-01 — IFD0 held orientation, resolution and
the Exif pointer, nothing else; no GPS IFD at all). A photo shared from the
library usually keeps its GPS. So a post's pin is resolved **at post time**
from the first of these that exists, and stored on the post as
`lat`/`lng`/`pin_source`:

1. **`exif`** — the photo's own GPS IFD. The form reads it **in the browser**
   before you post — a hand-rolled parser in `static/js/post.js` over the
   first 256 KB of the chosen file (APP1 → TIFF → IFD0 → GPS IFD; no library,
   no CDN) — and shows *photo GPS: 34.2100, -118.5000* or *none in this file*.
   Those parsed numbers are posted as `photo_lat`/`photo_lng` **for display
   only**; the server re-reads the EXIF out of the uploaded file and uses its
   own value.
2. **`browser`** — the phone's location. The form asks
   `navigator.geolocation` once on load (https; Safari prompts once per
   site), rounds to 4 decimals and posts it as hidden fields with the
   reported accuracy (`accuracy_m`). The status line says
   *location: on (±N m)* or *location: off — denied*.
3. **`checkpoint`** — the checkpoint chosen on the form. A fuzzed
   checkpoint gives a fuzzed pin. The map popup says *near <checkpoint>*.
4. **`none`** — nothing chosen, nothing found: timeline only, no pin.

The form shows those four as a **radio group**, pre-selected in exactly that
order, so a post where nothing is touched behaves as it always did. Picking
one instead (`pin_source=exif|browser|checkpoint|none`) overrides the ladder;
if the chosen source turns out to have no coordinates the server falls back
down the ladder and the log line says so. **No pin** and the older
`hide_location` field mean the same thing and both are accepted.

**Fuzz location** is the privacy control, and it is on every post:

- **Ticked** — the published point is snapped to the 0.05° grid and the
  accuracy is dropped. If it falls in a `precision: "fuzzed"` checkpoint's
  cell the post is stored as that checkpoint's snapped point and the popup
  says *near <checkpoint>*; anywhere else — a friend's driveway, somewhere
  with no checkpoint at all — it is a nameless cell.
- **Unticked** — the exact point and its accuracy are stored.
- Its **default is per post**: ticked when the point falls inside or within
  2 km of a fuzzed checkpoint, clear everywhere else. `PIN_PRECISION=exact`
  flips that one default; it means nothing anywhere else. Kris ruled the
  shape on 2026-09-02: *"protect personal locations, be precise with public
  locations"*.

The form says which case it is in before you post — *publishes as: near
<checkpoint> — a 5 km cell, not the point (this is a private place)* or
*publishes as: 34.2100, -118.5000 — the exact point, not a private place* —
and the line updates live when the toggle flips. `posts.pin_fuzzed` records
the outcome so a fuzzed (or an exact) post is findable later.

The same rule ran once at boot over posts pinned before it existed
(`migrate: post N … snapped to checkpoint …`, meta key
`pins_fuzzed_near_private_v1`); the photo row's own EXIF is never touched
and never rendered.

Posts made before this rule existed were pinned once, at boot, by the same
ladder — EXIF, else the chosen checkpoint, else the first checkpoint of the
post's leg — and the boot log says which (`migrate: post N pinned …`). That
backfill is keyed in the `meta` table and never runs twice.

### After the fact

A **signed-in** viewer sees one extra line under every post card saying what
the photo carried and what the site published — *pin: photo GPS 34.2100,
-118.5000 → published fuzzed, near <checkpoint>*, or *pin: phone ±11 m →
published exact 34.2100, -118.5000*. It is gated on `.Authed` in
`templates/base.html` and an anonymous render never contains a raw
coordinate. Beside it is a small **re-pin** control (`POST
/admin/post/<id>/pin`): choose the source again from what that post still has
— its photo's GPS, its checkpoint, or no pin — with the same fuzz toggle. A
browser fix is not re-derivable after the fact, so it is not offered; leaving
the source alone keeps it.

Beside it is **Edit** (`/admin/post/<id>/edit`), for the metadata a post can
still be wrong about once it is up: its **kind**, its **leg**, its
**checkpoint**, the **zone** and **time** it claims, and its words. Filed the
lunch stop under the wrong leg, or posted a Meal as a Note — that is this
screen, not a delete and re-post.

Three rules make it safe to use mid-drive:

- **A checkpoint is the more specific fact.** Choosing one moves the post to
  that checkpoint's leg and adopts its zone, exactly as the post form does, so
  the two selects can never end up contradicting each other.
- **The pin is not edited here.** It has its own control, right above, and a
  metadata edit must not quietly undo a deliberate re-pin. The one exception is
  a pin that came *from* a checkpoint: that follows the checkpoint you choose,
  or the post would name one place and pin another. A photo's own GPS, a phone
  fix, and the fuzz posture on either are left exactly as they are.
- **The zone re-stamps the wall clock, it does not shift the instant.** Moving
  a post from Central to Mountain means "that happened at 3 PM Mountain", not
  "that happened an hour earlier" — `<input type="datetime-local">` carries no
  zone of its own, so the time is re-read in whichever zone the select names.

The photo is shown for recognition but is not editable: media is
content-addressed and the store stays append-only, so replacing a photo is a
delete and a re-post.

## Shipping it

Two GitHub Actions workflows, and the split between them is the point.

**`CI`** (`.github/workflows/ci.yml`) runs on every push and pull request:
`gofmt`, `go vet`, `go build`, `go test -race`, and a build of the production
Docker image — because prod ships the *image*, so an image that does not build
is a broken deploy however green the Go job is. It touches no secret and cannot
reach the live ride.

**`Deploy`** (`.github/workflows/deploy.yml`) is `workflow_dispatch` **only**.
There is deliberately no push trigger: this site is live under someone's thumb
from a car, and auto-releasing every commit would put an unreviewed build in
front of the ride with no explicit go.

```bash
# PLAN — every gate, and the image built on Fly's remote builder, WITHOUT
# releasing it. The default, so a slip of the finger does not reach the ride.
gh workflow run Deploy -R clodemode/RoadTrip --ref trunk -f mode=plan

# APPLY — the same gates, the release, then an INDEPENDENT probe that the
# instance serves. A deploy's own exit code is not a state report.
gh workflow run Deploy -R clodemode/RoadTrip --ref trunk -f mode=apply
```

The gates re-run in the deploy workflow rather than trusting CI: a deploy is
dispatched against a ref, and "trunk was green yesterday" is not a statement
about the commit being shipped. The probe hits `/healthz` on the `.fly.dev`
host, which is the one path exempt from both the canonical-host 301 and the
`SITE_VISIBILITY=private` gate — so it answers even while the site is closed.

The image keeps the platform path standard: the binary is `/app/roadtrip`
(`WORKDIR /app`) and everything persistent is under `/data`.
[`APP_PROFILE.yaml`](APP_PROFILE.yaml) at the repo root declares those paths
and the Fly app, and is where the CLODE harness reads them from.

**One-time setup:** a deploy token in the repo secret `FLY_API_TOKEN`.

```bash
fly tokens create deploy -a roadtrip-clodemode
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
`/admin/private` behind the same `requireAuth` as every other admin route — the
same gate a person goes through, entered by machine, so there is no second auth
path to get wrong. Every mode proves the credential first with a
`GET /admin/posts.json`, which writes nothing. It does not check the repository
out: it talks to a running instance and has no use for the code.

The workflow logs **counts, never content**. The addresses are the whole point
of the overlay and never reach a run log.

### Editing lodging from the road

`/admin/lodging` is the same overlay as a form — every checkpoint's address and
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
ride's alone; `POST /admin/private?ride=<slug>` replaces that ride's block;
only a document that carries `"rides"` replaces the whole overlay.

⚠️ The two copies can drift. A `/admin/lodging` edit does not update
`PRIVATE_JSON`, and an `apply` replaces the first ride's block wholesale rather
than merging. After editing on the road, paste the result back into the secret.

### Working on it from Claude Code on the web

`.claude/hooks/session-start.sh` warms the Go module and build caches when a
web session starts, so `go build` / `go vet` / `go test` are ready immediately.
There is nothing else to install — the SQLite driver is pure Go and the tests
seed a throwaway database from the geojson the binary already embeds, so no
service and no network are needed at test time. The hook no-ops outside the web
sandbox (`$CLAUDE_CODE_REMOTE`), so a local checkout is untouched.

## Why it is built this way

A Go static binary on `scratch` with SQLite on a volume — a few MB, 256 MB
of RAM, no runtime dependencies, cold boot under a second. It is the
EagleLakeRocks pattern, chosen for the same reason: the app has to work from
a car on a hotspot, with nobody to patch anything. The Django pipeline it
replaces needed Docker, an MCP and an rsync on a Mac to publish; here the
phone is the client.

The Leaflet frontend is plain, dependency-free JavaScript.

## Licence

MIT.
