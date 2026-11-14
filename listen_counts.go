package main

// Listen Inn counts: how many people tuned in and how many songs they heard,
// without tracking anyone (Kris, 2026-11-14). A song is one audio 302. A
// listener is a visitor told apart from the others that day by a hash of the
// address salted with a random value made fresh each UTC day. The salt and the
// hashes live in memory only and are dropped when the day turns (or the
// process ends); neither is ever written to the database, a file or a log, and
// the hash is never shown. What is stored is two totals per ride per day.
// The signed-in trip-keeper is not counted. A restart in the middle of a day
// forgets who was seen, so one visitor can count twice that day; that is the
// price of keeping nothing.

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type listenCounter struct {
	mu   sync.Mutex
	day  string
	salt []byte
	seen map[[sha256.Size]byte]struct{}
}

// firstToday reports whether this address is new to the day of now, and keeps
// only a salted hash of it. A new UTC day gets a new salt and an empty set.
func (c *listenCounter) firstToday(addr string, now time.Time) (day string, first bool) {
	day = now.UTC().Format("2006-01-02")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.day != day || c.salt == nil {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return day, false // no randomness: count the song, not a listener
		}
		c.day, c.salt, c.seen = day, salt, map[[sha256.Size]byte]struct{}{}
	}
	h := sha256.Sum256(append(append([]byte{}, c.salt...), addr...))
	if _, ok := c.seen[h]; ok {
		return day, false
	}
	c.seen[h] = struct{}{}
	return day, true
}

func (a *App) listenClock() time.Time {
	if a.clockFn != nil {
		return a.clockFn()
	}
	return time.Now()
}

// countListen is called once per audio 302 for a visitor. The address never
// leaves this function except as the input of the hash.
func (a *App) countListen(r *http.Request) {
	if a.auth.IsAuthed(r) {
		return // the trip-keeper's own plays are not the audience
	}
	day, first := a.listenCount.firstToday(clientAddr(r), a.listenClock())
	listeners := 0
	if first {
		listeners = 1
	}
	if err := a.store.AddListen(a.activeSlug(), day, 1, listeners); err != nil {
		log.Printf("WARN listen count: %v", err)
	}
}

func (s *Store) AddListen(ride, day string, songs, listeners int) error {
	_, err := s.db.Exec(`INSERT INTO listen_counts (ride_slug,day,songs,listeners) VALUES (?,?,?,?)
	  ON CONFLICT(ride_slug,day) DO UPDATE SET songs=songs+excluded.songs, listeners=listeners+excluded.listeners`,
		ride, day, songs, listeners)
	return err
}

// ListenTotals sums a ride's days. A person who tuned in on two days counts on
// both: telling them apart across days would take the memory this keeps nothing for.
func (s *Store) ListenTotals(ride string) (listeners, songs int, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(SUM(listeners),0), COALESCE(SUM(songs),0) FROM listen_counts WHERE ride_slug=?`, ride).
		Scan(&listeners, &songs)
	return
}

// listenLine is the trip page's sentence, or "" when nobody has listened.
func (a *App) listenLine(ride string) string {
	l, s, err := a.store.ListenTotals(ride)
	if err != nil || (l == 0 && s == 0) {
		return ""
	}
	people, songs := "people", "songs"
	if l == 1 {
		people = "person"
	}
	if s == 1 {
		songs = "song"
	}
	return fmt.Sprintf("%d %s tuned in, %d %s heard", l, people, s, songs)
}
