package main

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

// A track mark is the trip-keeper saving music for later: a favourite track, or
// a bookmark at one point in a song. It lives on the server, one list per
// signed-in user, and is read and removed on the Plan page.
type TrackMark struct {
	ID        int64
	Kind      string
	Album     string
	Artist    string
	Title     string
	PositionS float64
}

// At is the bookmark's point in the song, as m:ss.
func (m TrackMark) At() string {
	s := int(m.PositionS)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (s *Store) AddTrackMark(user, kind string, albumID int64, album, artist string, idx int, title string, pos float64, now time.Time) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO track_marks(user, kind, album_id, album, artist, track_idx, title, position_s, created_at)
VALUES(?,?,?,?,?,?,?,?,?)`, user, kind, albumID, album, artist, idx, title, pos, now.UTC().Format(time.RFC3339))
	return err
}

func (s *Store) TrackMarks(user string) ([]TrackMark, error) {
	rows, err := s.db.Query(`SELECT id, kind, album, artist, title, position_s FROM track_marks WHERE user = ? ORDER BY id DESC`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackMark
	for rows.Next() {
		var m TrackMark
		if err := rows.Scan(&m.ID, &m.Kind, &m.Album, &m.Artist, &m.Title, &m.PositionS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTrackMark(user string, id int64) error {
	_, err := s.db.Exec(`DELETE FROM track_marks WHERE id = ? AND user = ?`, id, user)
	return err
}

func (a *App) trackMarkAdd(w http.ResponseWriter, r *http.Request) {
	kind := r.PostFormValue("kind")
	id, e1 := strconv.ParseInt(r.PostFormValue("album_id"), 10, 64)
	idx, e2 := strconv.Atoi(r.PostFormValue("track_idx"))
	pos, e3 := strconv.ParseFloat(r.PostFormValue("position_s"), 64)
	if (kind != "favourite" && kind != "bookmark") || e1 != nil || e2 != nil || e3 != nil || id <= 0 || idx < 0 || idx > 999 || math.IsNaN(pos) || math.IsInf(pos, 0) || pos < 0 {
		http.Error(w, "bad mark", http.StatusBadRequest)
		return
	}
	if err := a.store.AddTrackMark(a.userOf(r), kind, id, clip(r.PostFormValue("album"), 200), clip(r.PostFormValue("artist"), 200), idx, clip(r.PostFormValue("title"), 200), pos, time.Now()); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) trackMarkDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.store.DeleteTrackMark(a.userOf(r), id); err != nil {
		http.Error(w, "could not remove", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/plan", http.StatusSeeOther)
}
