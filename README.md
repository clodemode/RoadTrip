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
| `RIDE_SLUG` | which seeded ride `/` shows; default `atx-sjc-2026-09` |
| `DATA_DIR` | default `/data`; holds `roadtrip.sqlite3`, `media/`, `tmp/` (upload spill, swept on boot) and the optional `private.json` |
| `GOMEMLIMIT` | optional; unset ⇒ the GC's soft limit is 75 % of the cgroup limit or MemTotal (logged at boot as `memory limit: …`) |
| `$DATA_DIR/private.json` | private overlay, `{"checkpoints":{"<ord>":{"address":"…","notes":"…"}}}` keyed by checkpoint ord; rendered on admin pages only, never in the repo or the seed; absent = no private data |
| `SITE_URL` | the canonical origin, e.g. `https://roadtrip.opeongo.net`; unset (the local default) ⇒ no redirect and no canonical/og tags |
| `SITE_VISIBILITY` | `public` (default in code) or `private`; **private puts the whole site behind the one admin password** — see below |
| `PIN_PRECISION` | `snap` (default) or `exact`; decides only whether the post form's **Fuzz location** toggle starts ticked at a private place |
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
notes, editable from the phone. It is the everyday path; the carriage is for
restoring a volume and for keeping a canonical copy off it. Clearing both
fields for a checkpoint removes its entry, which is how a stale address goes
away.

Both writers go through `savePrivate`, which writes a temp file alongside the
overlay and renames it. The overlay is re-read on *every* admin request, so a
half-written file would break the pages that exist to show it; a rename within
the directory is the one write a concurrent reader cannot catch mid-way.

⚠️ The two copies can drift. A `/admin/lodging` edit does not update
`PRIVATE_JSON`, and an `apply` replaces the document wholesale rather than
merging. After editing on the road, paste the result back into the secret.

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
