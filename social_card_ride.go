package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The trip card for a ride: what it shows, the hash that is its address, and
// the handler that serves it. Every point comes from the legs the store hands
// out (loadRideSlug), the same ones a visitor's map draws, so a fuzzed
// checkpoint is already snapped and a line already masked; nothing here reads
// a raw coordinate, a post, a photo, a position or the private overlay.
// spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.

var errCardUndrawable = errors.New("the ride has no line to draw")

// cardCache keeps, per ride, the last spec with its hash (keyed by a
// fingerprint of the data it was built from) and the last PNG drawn.
type cardCache struct {
	mu   sync.Mutex
	memo map[string]cardMemo
	png  map[string]cardPNG
}

type cardMemo struct {
	fp   string
	spec *cardSpec
	hash string
}

type cardPNG struct {
	hash string
	b    []byte
}

// rideCard is the card for a ride: what it shows and the hash of that.
func (a *App) rideCard(slug string) (*cardSpec, string, error) {
	rv, err := a.loadRideSlug(slug)
	if err != nil {
		return nil, "", err
	}
	fp := a.cardFingerprint(rv)
	a.cards.mu.Lock()
	if m, ok := a.cards.memo[slug]; ok && m.fp == fp {
		a.cards.mu.Unlock()
		return m.spec, m.hash, nil
	}
	a.cards.mu.Unlock()
	spec, err := a.buildCardSpec(rv)
	if err != nil {
		return nil, "", err
	}
	hash := spec.hash()
	a.cards.mu.Lock()
	if a.cards.memo == nil {
		a.cards.memo = map[string]cardMemo{}
	}
	a.cards.memo[slug] = cardMemo{fp, spec, hash}
	a.cards.mu.Unlock()
	return spec, hash, nil
}

// hash is a short digest of everything the card shows and of the drawing itself.
func (s *cardSpec) hash() string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(append([]byte("card/"+strconv.Itoa(cardRendererVersion)+"/"), b...))
	return hex.EncodeToString(h[:6])
}

// alt is the card in plain words, for og:image:alt.
func (s *cardSpec) alt() string {
	t := "The route of " + s.Name
	if s.Miles > 0 {
		t += ": " + thousands(s.Miles) + " miles"
		if s.Days > 0 {
			t += " over " + strconv.Itoa(s.Days) + " day" + map[bool]string{true: "", false: "s"}[s.Days == 1]
		}
	}
	return t
}

// cardFingerprint is what the page already loaded plus the plan's own inputs,
// cheap to compute: when it is unchanged the memoised spec is still right.
func (a *App) cardFingerprint(rv *rideView) string {
	h := sha256.New()
	fmt.Fprint(h, rv.Ride.Name, "|", rv.Ride.StartDate, "|", rv.Ride.EndDate, "|", a.tripOpened(rv.Ride.Slug))
	if fi, err := os.Stat(tripPath(a.dataDir, rv.Ride.Slug)); err == nil {
		fmt.Fprint(h, "|file|", fi.ModTime().UnixNano(), fi.Size())
	}
	if sel := a.store.StoredSelection(rv.Ride.Slug); sel != nil {
		b, _ := json.Marshal(sel)
		h.Write(b)
	}
	for _, l := range rv.Legs {
		fmt.Fprint(h, "|leg|", l.ID, l.Ord, l.Name, l.Date, l.EstMiles)
		h.Write(l.Line)
		for _, c := range l.Checkpoints {
			fmt.Fprint(h, "|cp|", c.ID, c.Mark, c.IsTerminal, c.Lat, c.Lng, c.ArrivedAt, c.SkippedAt)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildCardSpec turns the ride as a visitor may see it into a cardSpec.
func (a *App) buildCardSpec(rv *rideView) (*cardSpec, error) {
	slug := rv.Ride.Slug
	spec := &cardSpec{Name: rv.Ride.Name}

	finished := rideFinished(rv.Legs)
	active := rideActive(rv.Legs)
	if !finished && !active && a.tripOpened(slug) {
		active = true // an opened trip reads as on the road, as the banner does
	}
	cur := 0 // index of the leg the car is on
	if o := openStops(rv.Legs); len(o) > 0 {
		for i, l := range rv.Legs {
			if l.ID == o[0].LegID {
				cur = i
			}
		}
	}
	switch {
	case finished:
		spec.State = "Finished"
	case active:
		spec.State = "On the road · day " + strconv.Itoa(cur+1)
	default:
		spec.State = "Planned"
	}

	// figures: what the public Plan page computes, not a new sum
	start, end := rv.Ride.StartDate, rv.Ride.EndDate
	if st, err := a.loadPlanSlug(slug); err == nil {
		choices, from, _ := st.requested(url.Values{}, false)
		sum := st.Doc.summarize(choices, from)
		spec.Miles, spec.Days = int(math.Round(sum.TotalMiles)), sum.DayCount
		start, end = sum.StartDate, sum.EndDate
	}
	spec.Dates = cardDates(start, end)

	for i, l := range rv.Legs {
		var pts [][]float64
		if json.Unmarshal(l.Line, &pts) != nil {
			continue
		}
		cl := cardLeg{Ord: l.Ord, Dotted: active && i > cur}
		for _, p := range pts {
			if len(p) >= 2 {
				cl.Pts = append(cl.Pts, [2]float64{p[0], p[1]})
			}
		}
		if len(cl.Pts) < 2 {
			continue
		}
		spec.Legs = append(spec.Legs, cl)
		for _, c := range l.Checkpoints {
			if strings.HasPrefix(c.Mark, "d") && !c.IsStart { // a night's end, as the Plan map marks it (never the start)
				spec.Discs = append(spec.Discs, cardDisc{Ord: l.Ord, Label: c.Mark, Pt: [2]float64{c.Lng, c.Lat}})
			}
		}
	}
	if len(spec.Legs) == 0 {
		return nil, errCardUndrawable
	}
	thinLegs(spec)
	return spec, nil
}

// thinLegs drops vertices a visitor could not tell apart on a 1200 px card,
// always keeping each leg's first and last.
func thinLegs(spec *cardSpec) {
	minLng, maxLng, minLat, maxLat := 180.0, -180.0, 90.0, -90.0
	for _, l := range spec.Legs {
		for _, p := range l.Pts {
			minLng, maxLng = math.Min(minLng, p[0]), math.Max(maxLng, p[0])
			minLat, maxLat = math.Min(minLat, p[1]), math.Max(maxLat, p[1])
		}
	}
	kx := math.Cos((minLat + maxLat) / 2 * math.Pi / 180)
	tol := math.Max((maxLng-minLng)*kx, maxLat-minLat) / 800
	for i := range spec.Legs {
		pts := spec.Legs[i].Pts
		out := pts[:1:1]
		for _, p := range pts[1 : len(pts)-1] {
			q := out[len(out)-1]
			if math.Hypot((p[0]-q[0])*kx, p[1]-q[1]) >= tol {
				out = append(out, p)
			}
		}
		spec.Legs[i].Pts = append(out, pts[len(pts)-1])
	}
}

// cardDates writes a trip's days as "Fri May 1 to Sun May 3, 2030".
func cardDates(start, end string) string {
	s, err := time.Parse(dateLayout, start)
	if err != nil {
		return ""
	}
	e, err := time.Parse(dateLayout, end)
	if err != nil || !e.After(s) {
		return s.Format("Mon Jan 2, 2006")
	}
	if s.Year() == e.Year() {
		return s.Format("Mon Jan 2") + " to " + e.Format("Mon Jan 2, 2006")
	}
	return s.Format("Mon Jan 2, 2006") + " to " + e.Format("Mon Jan 2, 2006")
}

// cardVisible says a visitor may be told this ride exists: the ride the site
// shows and every past one, never a next trip (see ridepick.go).
func (a *App) cardVisible(slug string) bool {
	if slug == a.activeSlug() {
		return true
	}
	for _, c := range a.allRideChoices() {
		if c.Slug == slug {
			return c.When != whenNext
		}
	}
	return false
}

// cardAddress is og:image and og:image:alt for a ride's page, or ok=false when
// the page must name the fallback card.
func (a *App) cardAddress(slug string) (img, alt string, ok bool) {
	if slug == "" || !validSlug(slug) || !a.cardVisible(slug) {
		return "", "", false
	}
	spec, hash, err := a.rideCard(slug)
	if err != nil {
		return "", "", false
	}
	return a.siteURL + "/og/ride/" + slug + ".png?v=" + hash, spec.alt(), true
}

// rideCardPNG answers GET /og/ride/<slug>.png with the ride's current card;
// the ?v= hash is ignored. A ride a visitor may not see, or cannot be drawn,
// is a 404; the route is not in visibilityOpen, so a private site closes it.
func (a *App) rideCardPNG(w http.ResponseWriter, r *http.Request) {
	slug, ok := strings.CutSuffix(r.PathValue("file"), ".png")
	if !ok || !validSlug(slug) || !a.cardVisible(slug) {
		http.NotFound(w, r)
		return
	}
	spec, hash, err := a.rideCard(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.cards.mu.Lock()
	c, have := a.cards.png[slug]
	a.cards.mu.Unlock()
	if !have || c.hash != hash {
		b, err := encodeCard(spec)
		if err != nil {
			http.Error(w, "the card could not be drawn", http.StatusInternalServerError)
			return
		}
		c = cardPNG{hash, b}
		a.cards.mu.Lock()
		if a.cards.png == nil {
			a.cards.png = map[string]cardPNG{}
		}
		a.cards.png[slug] = c
		a.cards.mu.Unlock()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(c.b)))
	w.Write(c.b)
}
