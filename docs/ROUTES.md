# Routes

Every address the server answers, read from `routes()` in `handlers.go`.
"Signed in" means a session cookie or the `PUSH_TOKEN` bearer header (see
[Operations](OPERATIONS.md#pushing-with-the-token)). "On the ride" means
signed in as an admin, or as an editor assigned to the ride being shown.
While `SITE_VISIBILITY=private`, every route marked *open* needs a sign-in
too, except `/healthz`, `/robots.txt`, the login page and the two static
files it renders with.

<!-- LISTEN-INN-ROUTES: `GET /listeninn` and `GET /listeninn/song/{id}` are
     added by a separate change. Document them here when they land. -->

## Open to every visitor

| route | what it is |
|---|---|
| `GET /` | the ride: map, checkpoints, the posts from the road, and the Listen Inn panel when music is configured |
| `GET /legs/{slug}` | one leg: its map, checkpoints, candidate stops and posts |
| `GET /photos` | the photo gallery, grouped by leg |
| `GET /photo/{id}` | one photo, fitted to the screen |
| `GET /plan` | the plan: days, miles, hours and any route options |
| `GET /plan.json` | the same arithmetic as data; `?opt-<option>=<choice>` prices an alternative without saving it |
| `GET /tracks.json` | the tracks played on the active ride |
| `GET /tracks/{id}/audio` | a 302 to a short-lived address for one played track; 404 when `PUBLIC_LISTEN` is off or no library is configured |
| `GET /media/…` | stored photos and thumbnails; immutable, cached |
| `GET /static/…` | the embedded CSS, JavaScript and images |
| `GET /robots.txt` | excludes `/driver/` and `/media/` |
| `GET /healthz` | answers 200 on any host, whatever the visibility |
| `GET /driver/login`, `POST /driver/login`, `POST /driver/logout` | sign in and out |

## Signed in

| route | what it is |
|---|---|
| `GET /route.geojson` | legs, checkpoints, pinned posts and driven tracks as GeoJSON (on the ride) |
| `GET /driver` | the driver screen |
| `GET /driver/hud.json` | what the driver screen shows, as data |
| `POST /driver/telemetry` | the phone's reports about the page itself (no coordinates are kept) |
| `GET /driver/eta.json` | every ETA shown, scored against the arrival |
| `GET /driver/post`, `POST /driver/post` | the post form (on the ride) |
| `GET /driver/post/{id}/edit`, `POST /driver/post/{id}/edit` | edit a post's kind, leg, checkpoint, zone, time and words |
| `POST /driver/post/{id}/pin` | choose a post's pin source again |
| `POST /driver/post/{id}/fuzz`, `POST /driver/post/{id}/unfuzz` | fuzz or restore a post's published point |
| `POST /driver/post/{id}/delete` | delete a post |
| `POST /driver/post/{id}/photo/remove`, `POST /driver/post/{id}/photo/undo` | remove a post's photo, and bring it back within ten minutes |
| `GET /driver/posts`, `POST /driver/posts/photos/remove`, `POST /driver/posts/photos/undo` | the photo management page |
| `GET /driver/posts.json` | every post with its photo's identity (see [Taking a census](OPERATIONS.md#taking-a-census)) |
| `POST /driver/checkpoint/{id}/arrive` | stamp, re-stamp or clear an arrival (see [Made it to](DESIGN.md#made-it-to)) |
| `GET /driver/lodging`, `POST /driver/lodging` | private addresses and notes for the ride's stops |
| `POST /driver/private` | replace the private overlay by machine; an admin or the push token |
| `POST /driver/trip` | push a trip document (see [Trips](TRIPS.md)); an admin or the push token |
| `POST /driver/ride/active` | choose the ride the site shows; an admin or the push token |
| `GET /driver/rides.json` | every ride, its row counts, and which one is showing; an admin or the push token |
| `POST /driver/plan` | save a plan selection or start date |
| `POST /driver/routing` | add a routing point for the navigation link |
| `POST /driver/freelance/mode`, `…/dest`, `…/picks` | the free-drive mode: no plan, an optional destination |
| `GET /driver/roadtracks/search`, `GET /driver/roadtracks/album/{id}` | album search and one album's tracks |
| `POST /driver/roadtracks/state`, `…/mark`, `…/mark/{id}/delete`, `…/event` | what is playing, favourites, and player trouble |
| `POST /driver/sim/start`, `POST /driver/sim/stop`, `GET /driver/sim.json` | the simulated drive; start is a 404 unless `SIM_ALLOWED=1` |
| `GET /driver/melt.json` | the event log, when `MELT_FILE` is set; admins only |
| `GET /chester`, `POST /chester/ask` | the trip helper (see [Chester](CHESTER.md)) |
| `GET /chester/preferences`, `POST /chester/preferences` | the traveller's standing preferences |
| `GET /driver/chester/memory`, `POST /driver/chester/memory/clear` | what Chester remembers about the signed-in traveller's tastes, and clearing it |
| `GET /driver/chester/log`, `GET /driver/chester/log.json`, `POST /driver/chester/log/{id}` | every question, its cost, and the verdict on it |

## Admins only

| route | what it is |
|---|---|
| `GET /driver/users`, `POST /driver/users` | list and create users |
| `POST /driver/users/{name}/reset`, `…/disable`, `…/enable`, `…/rides` | reset a password, disable or enable a user, assign rides |

The push token does not open the user routes.
