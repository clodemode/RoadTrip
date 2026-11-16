# Photos, pins and privacy

What RoadTrip publishes about where you are, and what it never does. Start with the [README](../README.md).

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
   no CDN) — and shows *photo GPS: 34.2100, -118.5000* or *no GPS, normal for a camera shot*.
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

**An old photo with no GPS** (its own EXIF time more than 20 minutes before
now, no GPS of its own, not a Start) is not where the phone is, so the ladder
above does not run for it. It is pinned as **`trail`**: the published pin of an
earlier post of the ride (a photo's GPS, the phone or the simulated car; never
a hidden post, a checkpoint pin or another trail pin) nearest in time to the
photo's time, within 15 minutes; with none, the post has no pin. It keeps its
own time, never checks in or moves the car, carries the flags `old_photo` (and
`trail_pin`), and the notice and the signed-in pin line say how long ago it was
taken and where it was pinned or that it was left off the map. A trail point at
a private place is always fuzzed, whatever the toggle says, and keeps no exact
point for Unfuzz. No phone fix is stored anywhere (telemetry is recorded
with no coordinate), so earlier posts are the whole trail on every instance. A radio the driver touched (the form's `pin_auto` is empty) is
honoured for the pin alone; the page's own pre-selection (`pin_auto=1`) is not
a choice. No `pin_auto` at all (an older page, curl) reads a sent `pin_source`
as a choice. `features/old_photo_without_place.feature` states it.

**Fuzz location** is the privacy control, and it is on every post:

- **Ticked** — the published point is snapped to the 0.01° grid and the
  accuracy is dropped. If it falls in a `precision: "fuzzed"` checkpoint's
  cell the post is stored as that checkpoint's snapped point and the popup
  says *near <checkpoint>*; anywhere else — a friend's driveway, somewhere
  with no checkpoint at all — it is a nameless cell.
- **Unticked** — the exact point and its accuracy are stored.
- Its **default is per post**: ticked when the point falls inside or within
  1 km of a fuzzed checkpoint, clear everywhere else. `PIN_PRECISION=exact`
  flips that one default; it means nothing anywhere else. Kris ruled the
  shape on 2026-09-02: *"protect personal locations, be precise with public
  locations"*.

The form says which case it is in before you post — *publishes as: near
<checkpoint> — a 1 km cell, not the point (this is a private place)* or
*publishes as: 34.2100, -118.5000 — the exact point, not a private place* —
and the line updates live when the toggle flips. `posts.pin_fuzzed` records
the outcome so a fuzzed (or an exact) post is findable later.

**Fuzz This** (ruled 2026-11-08) is that toggle made one tap, during and after
an upload: a **Fuzz This** button on every post card with a pin (and beside the
notice after a check-in post), **Unfuzz** once it is fuzzed. Being signed in is
the trust; there is no device token. Fuzzing keeps the exact point in private
columns (`posts.exact_*`, read by no post select and rendered nowhere) so
Unfuzz restores it; the published pin and every page, file and log show only
the fuzzy one. A post's pin is told to Claude at its stored precision, so
exactly, unless the post is fuzzed or lies near a private stop; the car's live
position stays at three decimals, and a photo is still never sent.

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
/driver/post/<id>/pin`): choose the source again from what that post still has
— its photo's GPS, its checkpoint, or no pin — with the same fuzz toggle. A
browser fix is not re-derivable after the fact, so it is not offered; leaving
the source alone keeps it.

Beside it is **Edit** (`/driver/post/<id>/edit`), for the metadata a post can
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
matters on a small machine. **The photo row's own `lat`/`lng` is deliberately
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
Disallow: /driver/
Disallow: /media/
Allow: /
```

`/media/` is excluded because an original is a photo, not a document: it
should not turn up in an image search, and a file that has been indexed cannot
be un-indexed on the day someone changes their mind.

## The HEIC rule

**JPEG only.** There is no pure-Go HEIC decoder and this ships on `scratch`
with nothing to shell out to. On an iPhone set **Settings → Camera →
Formats → Most Compatible** before the trip, or share a photo as JPEG. A
non-JPEG upload is refused with exactly that message. Originals are stored
content-addressed under `media/YYYY/MM/<sha256-prefix>.jpg`; thumbnails are
800 px wide with the EXIF orientation applied. The photo's time comes from
EXIF `DateTimeOriginal`, read in the zone chosen on the form. The stored
original carries **no EXIF location** — see *Served originals carry no GPS*
above.

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
48 MP 4:4:4 at 163 MB, none killed. The `fly.toml` in this repository asks for 1024 MB. Each photo
logs one line (`photo <sha>: W×H MP MB, orientation, thumb`), and each post
logs its pin decision (`post N: pin browser ±12 m → snapped to checkpoint 1
(Hudson Bend (home))`, `pin exif → fuzzed to checkpoint 1 (Hudson Bend
(home))`, `pin exif (exact, fuzz off) at checkpoint 1 (…)`, `pin checkpoint 3
(…) (fallback)`, `pin none (hidden)`) — names, never coordinates — so a driveway GPS test is
provable from the server log even though the public map hides a home fix.

## A signed-in page is never cached

**A signed-in page is never cached.** `/plan`, like every page, is one
address for the visitor and the trip-keeper. Every HTML response to a signed-in
request (the session cookie or the push token) is sent with
`Cache-Control: private, no-store` and `Vary: Cookie`, set once where the pages
are rendered, so no cache keeps a page that carries private text or hands it to
the next visitor. The anonymous response carries neither header.

