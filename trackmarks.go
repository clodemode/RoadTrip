package main

import (
	"encoding/json"
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

// trackMarkAdd saves a favourite or a bookmark and answers with what it saved
// ({"saved": "<track title, else album name>"}), so the page can say it. A
// request that names no album is the phone saying "whatever I was listening
// to": the server's one resume record for the signed-in user stands in, so
// what is playing is never kept per tab. With no record either, nothing is
// saved and the answer is 404.
func (a *App) trackMarkAdd(w http.ResponseWriter, r *http.Request) {
	kind := r.PostFormValue("kind")
	if kind != "favourite" && kind != "bookmark" {
		http.Error(w, "bad mark", http.StatusBadRequest)
		return
	}
	user := a.userOf(r)
	var id int64
	var idx int
	var pos float64
	var album, artist, title string
	if r.PostFormValue("album_id") == "" {
		rv, err := a.store.PlayerState(user, time.Now())
		if err != nil {
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
		if rv == nil || rv.AlbumID <= 0 {
			http.Error(w, "nothing to save", http.StatusNotFound)
			return
		}
		id, idx, pos, album, artist = rv.AlbumID, rv.TrackIdx, rv.PositionS, rv.Album, rv.Artist
		if idx < 0 || idx > 999 || math.IsNaN(pos) || math.IsInf(pos, 0) || pos < 0 {
			http.Error(w, "bad mark", http.StatusBadRequest)
			return
		}
	} else {
		var e1, e2, e3 error
		id, e1 = strconv.ParseInt(r.PostFormValue("album_id"), 10, 64)
		idx, e2 = strconv.Atoi(r.PostFormValue("track_idx"))
		pos, e3 = strconv.ParseFloat(r.PostFormValue("position_s"), 64)
		if e1 != nil || e2 != nil || e3 != nil || id <= 0 || idx < 0 || idx > 999 || math.IsNaN(pos) || math.IsInf(pos, 0) || pos < 0 {
			http.Error(w, "bad mark", http.StatusBadRequest)
			return
		}
		album, artist, title = r.PostFormValue("album"), r.PostFormValue("artist"), r.PostFormValue("title")
	}
	album, artist, title = clip(album, 200), clip(artist, 200), clip(title, 200)
	if err := a.store.AddTrackMark(user, kind, id, album, artist, idx, title, pos, time.Now()); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	saved := title
	if saved == "" {
		saved = album
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"saved": saved})
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
