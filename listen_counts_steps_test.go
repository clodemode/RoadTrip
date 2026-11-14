package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/listen_inn_counts.feature. The library is the moStub of
// roadtracks_test.go; every address is a documentation address (RFC 5737).

type lcWorld struct {
	t     *testing.T
	app   *App
	mo    *moStub
	kris  *http.Cookie
	now   time.Time
	pub   string // the opaque name of the one play
	salts [][]byte
}

func initListenCountScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &lcWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = lcWorld{t: t, now: time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)}
		return ctx, nil
	})
	sc.Step(`^the trip is active, RoadTracks is on and a track has been played on it$`, func() error {
		w.app = activeTripApp(t)
		w.mo = newMO(t)
		w.app.tracks = newRoadtracks(rtKey, w.mo.srv.URL)
		w.app.clockFn = func() time.Time { return w.now }
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		rec := doAs(t, w.app, w.kris, "POST", "/driver/roadtracks/state", url.Values{"album_id": {"42"}, "album": {"Abbey Road"},
			"artist": {"The Beatles"}, "track_idx": {"1"}, "position_s": {"0.4"}, "title": {"Two"}, "year": {"1969"}, "started": {"1760000000001"}})
		if rec.Code != http.StatusNoContent {
			return fmt.Errorf("the state post answered %d", rec.Code)
		}
		if err := w.app.store.db.QueryRow(`SELECT pub_id FROM plays LIMIT 1`).Scan(&w.pub); err != nil {
			return err
		}
		return nil
	})
	sc.Step(`^the driver is signed in$`, func() error { return nil })
	sc.Step(`^the address ([0-9.]+) asks for the track's audio (\d+) times$`, func(addr string, n int) error {
		for i := 0; i < n; i++ {
			w.fetch(addr, w.pub, nil)
		}
		return nil
	})
	sc.Step(`^the address ([0-9.]+) asks for the track's audio once$`, func(addr string) error { w.fetch(addr, w.pub, nil); return nil })
	sc.Step(`^the signed-in driver asks for the track's audio (\d+) times$`, func(n int) error {
		for i := 0; i < n; i++ {
			w.fetch("192.0.2.1", w.pub, w.kris)
		}
		return nil
	})
	sc.Step(`^the address ([0-9.]+) asks for an audio name that is not a play$`, func(addr string) error {
		w.fetch(addr, "0123456789abcdef", nil)
		return nil
	})
	sc.Step(`^the next day comes$`, func() error {
		w.salts = append(w.salts, append([]byte{}, w.app.listenCount.salt...))
		w.now = w.now.Add(24 * time.Hour)
		return nil
	})
	sc.Step(`^the trip is finished$`, func() error {
		startTheDrive(t, w.app)
		rv, err := w.app.loadRide()
		if err != nil {
			return err
		}
		c := rideCheckpoints(rv.Legs)
		at := time.Now().Add(time.Hour)
		for _, x := range c[1 : len(c)-1] {
			if x.ArrivedAt == "" && x.SkippedAt == "" {
				if err := w.app.store.SetSkipped(x.ID, at, "missed"); err != nil {
					return err
				}
			}
		}
		if err := w.app.store.SetArrived(c[len(c)-1].ID, at, nil); err != nil {
			return err
		}
		rv, _ = w.app.loadRide()
		if !rideFinished(rv.Legs) {
			return fmt.Errorf("the ride is not finished")
		}
		return nil
	})
	sc.Step(`^the trip page says "([^"]*)"$`, func(line string) error {
		body := w.home()
		if !strings.Contains(body, `id="listen-count">`+line+"<") {
			return fmt.Errorf("the page does not say %q; it says %q", line, w.line(body))
		}
		return nil
	})
	sc.Step(`^the trip page says nothing about listeners$`, func() error {
		if body := w.home(); strings.Contains(body, "listen-count") {
			return fmt.Errorf("the page says %q", w.line(body))
		}
		return nil
	})
	sc.Step(`^nothing stored holds the address ([0-9.]+)$`, func(addr string) error {
		if w.app.store.db.QueryRow(`SELECT COUNT(*) FROM plays`).Scan(new(int)) != nil {
			return fmt.Errorf("cannot read the plays")
		}
		hits, scanned := w.everythingStoredHolds(addr)
		if scanned < 3 {
			return fmt.Errorf("only %d stored values were searched; this check would pass on nothing", scanned)
		}
		if len(hits) > 0 {
			return fmt.Errorf("the address is stored in %v", hits)
		}
		return nil
	})
	sc.Step(`^the counts are kept per ride per day as two totals$`, func() error {
		rows, err := w.app.store.db.Query(`PRAGMA table_info(listen_counts)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var cols []string
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var d any
			if err := rows.Scan(&cid, &name, &typ, &nn, &d, &pk); err != nil {
				return err
			}
			cols = append(cols, name)
		}
		if strings.Join(cols, ",") != "ride_slug,day,songs,listeners" {
			return fmt.Errorf("listen_counts holds %v", cols)
		}
		return nil
	})
	sc.Step(`^the day's salt is not the one from the day before$`, func() error {
		if len(w.salts) == 0 || len(w.app.listenCount.salt) == 0 || bytes.Equal(w.salts[0], w.app.listenCount.salt) {
			return fmt.Errorf("the salt did not change with the day")
		}
		return nil
	})
	sc.Step(`^the counts show (\d+) listeners and (\d+) songs across the two days$`, func(l, s int) error {
		gl, gs, err := w.app.store.ListenTotals(w.app.activeSlug())
		if err != nil {
			return err
		}
		if gl != l || gs != s {
			return fmt.Errorf("%d listeners, %d songs", gl, gs)
		}
		var days int
		_ = w.app.store.db.QueryRow(`SELECT COUNT(*) FROM listen_counts`).Scan(&days)
		if days != 2 {
			return fmt.Errorf("%d day rows, want 2", days)
		}
		return nil
	})
}

// fetch is one audio request from an address, as Fly's edge would label it.
func (w *lcWorld) fetch(addr, id string, c *http.Cookie) {
	req := httptest.NewRequest("GET", "/tracks/"+id+"/audio", nil)
	req.Header.Set("Fly-Client-IP", addr)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	w.app.routes().ServeHTTP(rec, req)
}

func (w *lcWorld) home() string {
	return doAs(w.t, w.app, nil, "GET", "/", nil).Body.String()
}

func (w *lcWorld) line(body string) string {
	i := strings.Index(body, `id="listen-count">`)
	if i < 0 {
		return ""
	}
	rest := body[i+len(`id="listen-count">`):]
	if j := strings.Index(rest, "<"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// everythingStoredHolds searches every value of every table and every file
// under the data directory (the database file, the melt file, media, logs) for
// the address. scanned counts what was searched, so an empty search is not a pass.
func (w *lcWorld) everythingStoredHolds(addr string) (hits []string, scanned int) {
	db := w.app.store.db
	tables, _ := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	var names []string
	for tables.Next() {
		var n string
		_ = tables.Scan(&n)
		names = append(names, n)
	}
	tables.Close()
	for _, n := range names {
		rows, err := db.Query(`SELECT * FROM "` + n + `"`)
		if err != nil {
			continue
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for _, v := range vals {
				scanned++
				if s := valueText(v); strings.Contains(s, addr) {
					hits = append(hits, "table "+n)
				}
			}
		}
		rows.Close()
	}
	_ = filepath.Walk(w.app.dataDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil {
			scanned++
			if bytes.Contains(b, []byte(addr)) {
				hits = append(hits, "file "+filepath.Base(p))
			}
		}
		return nil
	})
	return hits, scanned
}

func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case sql.RawBytes:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}
