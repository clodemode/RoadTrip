# Trips

How a trip gets onto a RoadTrip instance, the trip document, route options, and what a saved plan change may and may not touch. Start with the [README](../README.md).

## Trips on the volume

The repository is public; a trip is not — where it starts and ends, and when.
So a new trip is **not** added to `seed/`. It is one GeoJSON document on the
volume, `$DATA_DIR/trips/<slug>.geojson`, and it gets there by push:

```bash
H="Authorization: Bearer $PUSH_TOKEN"; S=https://your-site.example   # your own origin

# the whole trip document; parsed completely before a byte is written
curl -s -H "$H" -H 'Content-Type: application/geo+json' --data-binary @trip.geojson $S/driver/trip

# make it the ride the site shows (or add ?activate=1 to the push)
curl -s -H "$H" -X POST -d slug=<slug> $S/driver/ride/active
curl -s -H "$H" -X POST -d clear=1 $S/driver/ride/active     # back to RIDE_SLUG

# every ride in the database, its row counts, and which one is showing
curl -s -H "$H" $S/driver/rides.json
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
The same check runs on a saved selection. The embedded seed passes it (a test holds that), though boot does not run it
on an embedded seed.

**The active ride** is a row in the `meta` table, read per request. Setting it
moves every page, the post form and `/route.geojson` together. The ride that
stops being shown is not touched: it stays in the database whole.

## Options

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

A fork can change more than one thing at once: where the first night is spent
changes the road of day 1 *and* which day-2 roads make sense. So a feature may
carry `"when": {"day1": "pawilds", "night1": "charleston"}` and belong to the
plans where every option named holds that choice (`option` + `choice` is the
one-pair spelling of the same thing). An option may carry a `"when"` too:
`{"id": "day2", "when": {"night1": "greensburg"}, …}` is **in force** only
while Night 1 is Greensburg, and otherwise it is off the plan page and every
feature that names it is off the plan. An option may only name options
declared before it. A push checks the document under every combination of
choices (at most 64), so no combination the page can offer is a plan that
fails its checks.

A leg may carry `"day": N` instead of a date. It is then dated from the ride's
start date, and **the start date is part of the stored selection** — so a
departure that slips a day is one saved field, not an edit to every leg. With
`avg_mph` set, a leg's hours are its miles at that speed, and a day over
`long_day_hours` is flagged.

**Once a ride exists, the saved start date wins over the document's.** The
first push of a trip takes `ride.start_date` from the document. After that the
start date in force is the saved selection's, exactly like the saved choices,
and a document pushed again with a different `ride.start_date` does **not**
move it: the start moves through `POST /driver/plan` (`start=YYYY-MM-DD`) and
nowhere else. The push still succeeds, and says so. Its answer always carries
`start_date`, the date in force, and when the document's differs it also
carries `document_start_date` and `start_date_not_applied`, a sentence saying
the document's start was not applied and where the start is moved. The boot
pass logs the same fact, without the dates.

The database holds one plan: the document under the stored selection. The
alternatives stay in the file. `/plan` shows them all; switching there is local
to the visitor's browser, and only the signed-in trip-keeper's **Save this
plan** (`POST /driver/plan`, fields `opt-<option>=<choice>` and `start=`) changes
the plan for everyone. `GET /plan.json?opt-<option>=<choice>` answers "what
would that cost" without saving anything.

**A saved change never reaches what the road has made real.** It is refused
whole — and says why — if the new plan would drop a leg that holds posts,
re-route one that holds posts and has been driven to its last stop (or onto a
road that misses a stop the car has already reached), or drop, renumber or
replace a checkpoint that is arrived or holds posts. So a fork settled on the
road — Night 1 moved from Greensburg to Charleston at the border — is taken
with the day's posts and arrivals kept, because the leg row is refreshed in
place; a different road for a day already driven is not. Moving the start date
is never refused: it moves dates and nothing else. A re-pushed document is held
to the same rule.

**What a page says about a save is a fixed sentence.** After **Save this
plan** the page comes back to the line under the button: `Saved.`, or
`Not saved:` and why. The redirect carries a short code (`/plan?ok=saved`,
`/plan?error=conflict`), never words, and each page
maps the codes it knows to its own sentences; a code it does not know prints
nothing. `/plan` is a public page, and free text in a query
string would let anyone put a sentence of their choosing on the site with a
link. curl is unaffected: it gets the status and the full reason as JSON.

**Every coordinate on `/plan` has been through the store's fuzz seam**, the
roads not taken included: a `precision: "fuzzed"` checkpoint is its 0.01° cell,
and no line is drawn within 1 km of one — the line runs to the cell point
instead. The same mask now covers the ride pages and `/route.geojson`, which
used to hand back the un-snapped vertex where a leg met a private checkpoint.

## Converting a planning tool's route

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
planning-handoff line. Keep your own trip documents out of your fork the same
way: they belong on the volume.

