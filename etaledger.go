package main

// The ETA ledger: every arrival time the driver screen shows is written here,
// once per five minutes per stop, and scored when the stop is marked arrived.
// It holds names and times only, never a position.

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"
)

// etaDedupe is how long a stop's prediction stands before another is kept.
const etaDedupe = 5 * time.Minute

type etaRow struct {
	ID               int64
	RideSlug         string
	CheckpointID     int64
	CheckpointName   string
	User             string
	PredictedAt      time.Time
	PredictedArrival time.Time
	Source           string
	ActualArrival    *time.Time
	ErrorMin         *int
}

// aboutClock is an arrival time as the screen says it: to the nearest five
// minutes (half up), in the given zone, "about 4:35 PM EST". A zone with no
// name of its own (a bare offset) is left unsaid rather than printed as -0600.
func aboutClock(t time.Time, loc *time.Location) string {
	t = t.In(loc).Add(150 * time.Second)
	t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()-t.Minute()%5, 0, 0, loc)
	out := "about " + t.Format("3:04 PM")
	if z := t.Format("MST"); z != "" && !strings.HasPrefix(z, "+") && !strings.HasPrefix(z, "-") {
		out += " " + z
	}
	return out
}

// RecordETA keeps a prediction unless one for the same ride, stop and driver
// was written in the last five minutes. It reports whether it wrote.
func (s *Store) RecordETA(ride string, cp int64, user string, at, arrival time.Time, source string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM eta_predictions WHERE ride_slug=? AND checkpoint_id=? AND user=? AND predicted_at>?`,
		ride, cp, user, utcString(at.Add(-etaDedupe))).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, err := s.db.Exec(`INSERT INTO eta_predictions (ride_slug,checkpoint_id,user,predicted_at,predicted_arrival,source) VALUES (?,?,?,?,?,?)`,
		ride, cp, user, utcString(at), utcString(arrival), source)
	return err == nil, err
}

const etaCols = `p.id,p.ride_slug,p.checkpoint_id,COALESCE(c.name,''),p.user,p.predicted_at,p.predicted_arrival,p.source,p.actual_arrival,p.error_min`

func (s *Store) etaQuery(q string, args ...any) ([]etaRow, error) {
	rows, err := s.db.Query(`SELECT `+etaCols+` FROM eta_predictions p LEFT JOIN checkpoints c ON c.id=p.checkpoint_id `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []etaRow
	for rows.Next() {
		var r etaRow
		var pa, par string
		var act *string
		if err := rows.Scan(&r.ID, &r.RideSlug, &r.CheckpointID, &r.CheckpointName, &r.User, &pa, &par, &r.Source, &act, &r.ErrorMin); err != nil {
			return nil, err
		}
		r.PredictedAt, _ = parseUTC(pa)
		r.PredictedArrival, _ = parseUTC(par)
		if act != nil {
			if t, ok := parseUTC(*act); ok {
				r.ActualArrival = &t
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScoreETA fills the actual arrival and the error (minutes late, negative if
// early) on every unscored prediction for the stop, and returns them.
func (s *Store) ScoreETA(ride string, cp int64, actual time.Time) ([]etaRow, error) {
	open, err := s.etaQuery(`WHERE p.ride_slug=? AND p.checkpoint_id=? AND p.actual_arrival IS NULL ORDER BY p.predicted_at, p.id`, ride, cp)
	if err != nil {
		return nil, err
	}
	for i := range open {
		e := int(math.Round(actual.Sub(open[i].PredictedArrival).Minutes()))
		if _, err := s.db.Exec(`UPDATE eta_predictions SET actual_arrival=?, error_min=? WHERE id=?`, utcString(actual), e, open[i].ID); err != nil {
			return nil, err
		}
		a := actual
		open[i].ActualArrival, open[i].ErrorMin = &a, &e
	}
	return open, nil
}

// ETALedger lists a ride's predictions, newest first.
func (s *Store) ETALedger(ride string) ([]etaRow, error) {
	return s.etaQuery(`WHERE p.ride_slug=? ORDER BY p.predicted_at DESC, p.id DESC LIMIT 500`, ride)
}

// etaScore is the scored count and the share of those within ten minutes.
func etaScore(rows []etaRow) (n int, within10 float64) {
	hit := 0
	for _, r := range rows {
		if r.ErrorMin == nil {
			continue
		}
		n++
		if *r.ErrorMin >= -10 && *r.ErrorMin <= 10 {
			hit++
		}
	}
	if n > 0 {
		within10 = float64(hit) / float64(n)
	}
	return n, within10
}

type etaOut struct {
	Checkpoint       string `json:"checkpoint"`
	PredictedAt      string `json:"predicted_at"`
	PredictedArrival string `json:"predicted_arrival"`
	ActualArrival    string `json:"actual_arrival,omitempty"`
	ErrorMin         *int   `json:"error_min"`
	Source           string `json:"source"`
}

// etaJSON is GET /driver/eta.json: the ledger and how well it did.
func (a *App) etaJSON(w http.ResponseWriter, r *http.Request) {
	ride := a.activeSlug()
	rows, err := a.store.ETALedger(ride)
	if err != nil {
		http.Error(w, "the ledger could not be read", http.StatusInternalServerError)
		return
	}
	out := make([]etaOut, 0, len(rows))
	for _, e := range rows {
		o := etaOut{Checkpoint: e.CheckpointName, PredictedAt: utcString(e.PredictedAt),
			PredictedArrival: utcString(e.PredictedArrival), ErrorMin: e.ErrorMin, Source: e.Source}
		if e.ActualArrival != nil {
			o.ActualArrival = utcString(*e.ActualArrival)
		}
		out = append(out, o)
	}
	n, rate := etaScore(rows)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ride": ride, "predictions": out, "hit_rate_10": rate, "n": n})
}
