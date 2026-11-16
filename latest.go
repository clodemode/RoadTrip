package main

// The latest at a glance. Every list on the site reads oldest first, so a
// visitor who lands mid-trip would have to scroll past days of history to find
// where it is now. The first screen of the Ride page answers that instead, in
// a strip of three small tiles: the latest point, the latest song, the latest
// photo (Kris, 2026-11-16). Each tile links to its item. A tile with nothing to
// show is absent, and a ride with none of the three has no strip.
//
// The point tile is words only: a place name and a time in that place's own
// zone. It carries no coordinate, so there is nothing here for the fuzz seam
// to protect, and a post that hides its location is never the latest point.

import (
	"strconv"
	"time"
)

// latestTile is one tile. Name is the bold line; Sub is the smaller one.
type latestTile struct {
	Label string
	Name  string
	Sub   string
	Href  string
	Thumb string // media-relative thumbnail, the photo tile only
}

type latestView struct {
	Point, Song, Photo *latestTile
}

// LatestPlay is a ride's newest play, or nil when it has none.
func (s *Store) LatestPlay(ride string) (*Play, error) {
	rows, err := s.db.Query(`SELECT id, pub_id, album, artist, year, title, started_at, tz FROM plays
WHERE ride_slug = ? ORDER BY started_at DESC, id DESC LIMIT 1`, ride)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var p Play
	if err := rows.Scan(&p.ID, &p.PubID, &p.Album, &p.Artist, &p.Year, &p.Title, &p.StartedAt, &p.TZ); err != nil {
		return nil, err
	}
	return &p, rows.Err()
}

// latestPoint is the newest of the stamped arrivals and the pinned posts. An
// arrival names its stop; a post names the stop it was made at, or says it was
// on the road. A hidden or unpinned post says nothing about where the car is.
func latestPoint(legs []Leg, posts []Post) *latestTile {
	var best *latestTile
	var bestAt time.Time
	take := func(at, tz, name, href string) {
		t, ok := parseUTC(at)
		if !ok || (best != nil && !t.After(bestAt)) {
			return
		}
		best, bestAt = &latestTile{Label: "Latest point", Name: name, Sub: localFmt(at, tz, fmtLocal), Href: href}, t
	}
	for _, c := range rideCheckpoints(legs) {
		if c.ArrivedAt != "" {
			take(c.ArrivedAt, c.TZ, c.Name, "#cp-"+strconv.FormatInt(c.ID, 10))
		}
	}
	for _, p := range posts {
		if p.HideLocation || p.PinLat == nil {
			continue
		}
		name := p.CheckpointName
		if name == "" {
			name = "On the road"
		}
		take(p.OccurredAt, p.TZ, name, "#post-"+strconv.FormatInt(p.ID, 10))
	}
	return best
}

// latestFor builds the strip for the Ride page from posts in the order the
// page lists them (oldest first). nil means there is nothing to show.
func (a *App) latestFor(rv *rideView, posts []Post, now time.Time) *latestView {
	v := &latestView{Point: latestPoint(rv.Legs, posts)}
	if pl, err := a.store.LatestPlay(rv.Ride.Slug); err == nil && pl != nil {
		label := "Last played"
		if at, ok := parseUTC(pl.StartedAt); ok && !rideFinished(rv.Legs) && now.Sub(at) < nowPlayingFresh && now.Sub(at) > -time.Minute {
			label = "Now playing"
		}
		sub := localFmt(pl.StartedAt, pl.TZ, fmtLocal)
		if pl.Artist != "" {
			sub = pl.Artist + " · " + sub
		}
		v.Song = &latestTile{Label: label, Name: pl.Title, Sub: sub, Href: "/listeninn"}
	}
	for i := len(posts) - 1; i >= 0; i-- {
		if p := posts[i]; p.Photo != nil {
			v.Photo = &latestTile{Label: "Latest photo", Name: p.Title, Sub: localFmt(p.OccurredAt, p.TZ, fmtLocal),
				Href: "/photo/" + strconv.FormatInt(p.ID, 10), Thumb: p.Photo.ThumbPath}
			break
		}
	}
	if v.Point == nil && v.Song == nil && v.Photo == nil {
		return nil
	}
	return v
}
