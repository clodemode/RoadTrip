# Design notes

How RoadTrip behaves and why. The [README](../README.md) is the front door; this page is the long-form record behind it.

## The pages

- `/` — the ride: hero with the day-of-trip badge, a map (leg polylines,
  numbered checkpoint pins, muted candidate stops, photo pins), the
  checkpoint list with arrival stamps, and the timeline of posts.
- `/legs/<slug>` — one leg: its map, checkpoints, candidate stops, posts.
- `/photos` — the gallery, grouped by leg.
- `/route.geojson` — signed-in drivers only (session cookie or `PUSH_TOKEN` bearer; a visitor is sent to sign-in, and the masthead link shows only when signed in): legs, checkpoints and pinned posts as GeoJSON,
  CORS `*`, plus a `kind=track` line per leg for what was actually driven.
- `/plan` — the planner: the days with miles, hours and a long-day flag, the
  trip totals, and — for a trip that carries route options — each option as a
  control, with the roads not taken drawn dashed on the map. `/plan.json` is
  the same arithmetic as data. See [Trips](TRIPS.md).
- `/driver/post` — **Post a Photo**, the driver's one screen: photo, kind,
  title, body, leg, zone, hide-location. By default it posts just a photo;
  before the drive starts the one choice is "Start the drive from <start>", and
  afterwards a photo near the next open stop checks in there. The form has no
  milestone list. `POST /driver/checkpoint/{id}/arrive` is a repair tool with
  no page button except a small Undo on a settled row (arrived, left or
  skipped), which posts `clear=1` and needs no position. The start is a
  departure: no page offers navigation to it, and only the next open stop is a
  destination.
- `/driver/posts.json` — the census: **every** post with its photo's identity,
  signed-in only. See [Operations](OPERATIONS.md#taking-a-census).

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
fuzzed checkpoint to the 0.01° grid on the way out of the store, so no page,
pin or GeoJSON ever sees the real point. Street addresses never enter the
seed at all — they live in `$DATA_DIR/private.json` on the volume and show
only to the signed-in trip-keeper. See [Photos, pins and privacy](PRIVACY.md).

Planning data is **seed-owned until actualized**: on every boot the seed
refreshes legs, any checkpoint that has not been stamped as arrived, and the
candidate-stop set. Arrived checkpoints, posts and photos are never touched.
So a seed edit reaches the site on the next deploy.

## The driver screen, music and the free drive

These arrived after the first trip, and the feature files in `features/`
state each one's behaviour in full. In short:

- **`/driver`, the driver screen.** Large type for a phone on a dash mount:
  the next stop, miles left today, an ETA once the car is moving (see *ETA
  policy* below), a link that opens a navigation app at the next stop, and a
  line each for weather alerts and traffic on the road ahead. Weather comes
  from the US National Weather Service and, in Canada, Environment Canada,
  and needs no key. Traffic needs a TomTom key. A source that has no key,
  failed or timed out is shown as "unknown" with its reason, never as "clear".
- **The free drive ("FreeLance").** A drive with no plan: either a typed
  destination (searched and routed with TomTom) or none at all. It is kept
  per signed-in user in memory and never touches the plan.
- **Road Tracks.** An album search on the driver screen that plays music from
  a private music library, with a frame that keeps it playing as pages
  change. No audio passes through RoadTrip: the phone fetches it from the
  short-lived address the library hands back. It needs a library that speaks
  the API of MusicOrgan, the maintainers' own music server, so it is off for
  anyone else today.
- **The Listen Inn.** The public side of Road Tracks: the list of what played
  on the ride, with a play control for visitors when `PUBLIC_LISTEN` is on.
  A visitor can hear only tracks that were actually played on the active
  ride; there is no search.
- **Drive Tracks and podcasts.** One tap asks Chester to pick an album from
  the library for the road being driven, or a podcast episode (found with
  Apple's free podcast search, which needs no key).
- **Trail points.** A Chester question or a track play, when the phone sent a
  position, stores one published (fuzz-checked) point on the ride so the
  "been here" line follows the car between posts.

<!-- SIBLING-CHANGES: two changes are landing separately and are not described
     here yet. (1) The Listen Inn becomes a floating card, with the addresses
     /listeninn and /listeninn/song/{id}. (2) Every list of posts, photos and
     songs runs oldest first, and the ride page gains Latest Point, Song and
     Photo tiles. Fold their notes in here and in ROUTES.md when they merge. -->

## A finished trip is a record

Once a drive has begun and its finish is reached or skipped, the ride reads
*Finished* (`rideFinished`, the same state the banner shows; nothing extra is
stored). The site then stays up as a record and spends nothing (Kris,
2026-11-14): the map, the posts, the photos and the Listen Inn history stay open
to every visitor, but the live-drive parts are not drawn: no drive HUD strip, no
"Currently playing" chip, no ETA (the driver screen's gauge is hidden and the
HUD makes none), and no Waze or Navigate link. **Chester is resting**: a
question, a Drive Tracks tap or a FreeLance "Find picks" is refused with a fixed
sentence and makes no API call, and the Chester links leave the menu. A new,
unfinished ride made active turns all of it back on. `features/finished_trip_is_a_record.feature`
holds it.

## Made it to

An arrival is not only a time. `POST /driver/checkpoint/<id>/arrive` takes an
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
  wrong time, not a wrong place. It also clears a skip (`skipped_at` and
  `skip_reason`), so Undo works on a missed stop. This is the one thing the
  page's Undo link does.
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

## ETA policy

The driver screen makes no arrival promise until the car is moving. The phone says so with `moving=1` on `/driver/hud.json` (its speed above 2 m/s, or more than 200 m covered in the last two minutes; a server-held simulated drive is moving while its speed is above 0), and the ETA is worked out only then and only from a fix on the leg, never from a guess at today's leg. It is written "about 4:35 PM EST", rounded to the nearest five minutes. A fix further than 50 km from every leg is shown as "Off route, N mi from the plan" (the distance is worked out on the server), with no ETA and no miles done. Every ETA the screen shows is kept in the `eta_predictions` table, once per five minutes per stop, and scored when that stop is marked arrived: `/driver/eta.json` (signed in) lists the predictions with their error in minutes and the share within ten minutes, and each scoring publishes an `eta_hit` MELT record (stop name and minutes, no position). Spec: `features/eta_policy.feature`.

## Why it is built this way

A Go static binary on `scratch` with SQLite on a volume: an image of a few
MB, no runtime dependencies, cold boot under a second. The app has to work
from a car on a hotspot, with nobody to patch anything, and the phone is the
only client it needs.

The Leaflet frontend is plain, dependency-free JavaScript.

