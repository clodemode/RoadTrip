# Chester

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

**An answer always has words.** Chester is asked to show its places first and
write the answer after them, and that order is not a matter of taste: the
default model returns words written ahead of a tool call as a thinking block,
which arrives empty, so an answer written before its cards is never seen. When
the cards arrive with no words, the tool is answered with a request for the
whole answer, and that second request writes it. If the model still writes
nothing, the page is not left with a card and no sentence: the site builds one
fixed line from the cards' own names, shows it, and marks the row
(`answer_synthesized`) so it is never read as Chester's words. While it works,
the page is told *Thinking* or *Searching the web* once each, not once for
every step the API takes.

**Signed in only, and capped.** `/chester` and everything under it answer only
to the session or the push token, because every question spends money. One
question runs at a time, and a second is told that Chester is busy. A question
that fails leaves one sentence on the page and stays in the box.

**The daily limit.** Before each question the day's spending (the driver's day, in the ride's zone) is read
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
midnight where the ride is. A question the driver cancels is charged only what
the API had reported by then, and nothing when it had reported nothing; the
row's note says so. What a question reads is not in the worst case, because nothing
bounds it in advance, and a typical question costs a small part of these
figures. A limit below a mode's worst case refuses every question in that
mode, and boot logs a warning saying so.

**What a question costs** is worked out from the API's own usage for every
request the question took, at list prices, plus web searches at $10 per 1,000.
Each row keeps that usage as it arrived (`usage_json` in the export: the usage
object from the first event of each request and the one that ends it, counts
only, nothing of the question or the answer), and the row's tokens, searches
and dollars are worked out from that record and from nothing else, so the two
can always be checked against each other.

How the record is read. The usage that ends a request is the total for the
whole request. A request that ran a web search is several sampling steps, and
the response itemises them (`usage.iterations`); the steps add up to the
top-level counts exactly, so reading either gives the same tokens. The steps
name no model, so each is priced at the model that ran the request. The first
live question showed why that needs saying: its steps were priced as the
dearest model in the table and the row read $0.20 for tokens that cost $0.08.
When the API re-ran a declined question on another model, the itemised list
is the only full account: the top-level counts then cover the answering
attempt alone, the declined attempt is billed too, and each entry is priced at
the model it names. A model the price table does not know, and an entry of any
other kind that names no model, is priced as the dearest one in the table, so
the unknown can only reach the limit sooner. With no itemised list, each price
takes the dearer of the model asked and the model that answered.

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
the same 0.01° cell a post's pin would get. The private overlay
and photos are never sent. The nearest checkpoint's town is
said only when that checkpoint's own name is one. The web search tool is given
the ride-day's time zone and nothing else: never a coordinate, and not the
town. The tools come first in the prompt, ahead of the plan, so a town or a
zone that moved with the car would discard the cached plan at every stop; the
town is in the question's own message instead, after the cached part.

**The key.** Chester needs an API key for the Claude API. The operator sets it
as a Fly secret named `ANTHROPIC_API_KEY`, by hand, with their own key; it is
not in this repository, not in `fly.toml`, and not in any log. For a local run
it goes in `.env`, which git and the image build ignore and the repo guard test
requires them to. Setting a secret
restarts the machine, and that restart is the only thing switching Chester on
takes. With no key the feature is absent: the
masthead has no link, `/chester` says Chester is not set up, and boot logs
`chester: disabled`. With one, boot logs `chester: enabled` and the two models.
`guard_test.go` fails the build on anything shaped like a key in the tree.

**The log.** `GET /driver/chester/log` lists every question: when and in which
zone, the mode, the question, the model and effort, milliseconds to the first
word and in all, tokens in, out, read from the cache and written to it,
searches, dollars (marked when they are an estimate), the answer or, for a
question that failed, what had been written, the places, how it stopped, and
the fuzzed position that was sent. The first-word time is the first word of
Chester's own text; when it wrote none the row has no such time, not a zero. A
verdict is one tap in place: **Right**, **Partly**, **Wrong** or
**Unverified**, with what it was checked against, a note, and an optional pair
of figures (Chester's and the reference, in minutes, dollars or miles). The
summary groups by mode and verdict and gives the count, the median time, the
dollars, and the mean absolute percent error where pairs exist.
`GET /driver/chester/log.json` is the whole log as data. Neither is reachable
without signing in, and nothing of the log is on any open page.

```bash
H="Authorization: Bearer $PUSH_TOKEN"; S=https://your-site.example   # your own origin

# ask from curl: the answer streams back one JSON object a line
curl -sN -H "$H" -d preset=fuel $S/chester/ask
curl -sN -H "$H" -d mode=plan --data-urlencode 'q=Where should we stay tomorrow night?' $S/chester/ask

# rule on question 12, and read the whole log
curl -s -H "$H" -d verdict=partly -d against=navigation -d chester_value=50 -d reference_value=40 -d unit=minutes \
     $S/driver/chester/log/12
curl -s -H "$H" $S/driver/chester/log.json
```

Chester only suggests. Nothing it says is written into the plan.

