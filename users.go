package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The users file lives on the volume ($DATA_DIR/users.json), like private.json:
// it holds password hashes and never belongs in the repo. It is rewritten whole
// through a temp file and a rename, so a crash leaves the old file or the new.

const (
	usersFile  = "users.json"
	roleAdmin  = "admin"
	roleEditor = "editor"
	minPwLen   = 6 // length is the only password rule; no complexity requirements
)

var usernameRE = regexp.MustCompile(`^[a-z0-9_-]{2,32}$`)

// validUsername: lowercase letters, digits, dash, underscore. No dot, because
// the session cookie joins its fields with one.
func validUsername(s string) bool { return usernameRE.MatchString(s) }

type User struct {
	Name     string    `json:"name"`
	Hash     string    `json:"hash"`
	Role     string    `json:"role"`
	Created  time.Time `json:"created"`
	Disabled bool      `json:"disabled,omitempty"`
	// Rides are the ride slugs a non-admin driver is assigned to. Admins are
	// implicitly on every ride and their list is ignored. nil (no "rides" key
	// in the file, or null) is a user from before assignment existed: they are
	// on whatever ride is active until an admin sets a list, and boot pins it
	// (migrateLegacyRides). An empty, non-nil list is a driver on no ride.
	Rides []string `json:"rides"`
}

// mayRide reports whether u may see and write ride slug; active is the ride
// the site is showing, which is the only ride a legacy (nil list) user has.
func (u User) mayRide(slug, active string) bool {
	if u.Role == roleAdmin {
		return true
	}
	if u.Rides == nil {
		return slug == active
	}
	for _, r := range u.Rides {
		if r == slug {
			return true
		}
	}
	return false
}

type userStore struct {
	mu    sync.RWMutex
	path  string // "" = memory only (tests)
	users map[string]User

	// seen is the file's mtime and size as this store last read or wrote it. A
	// different stat means another process (`roadtrip -set-password` over fly
	// ssh) changed the file, and the next get/list re-reads it: a password
	// change never needs a restart.
	seenMod  time.Time
	seenSize int64
	seenOK   bool
	warned   bool // a failed reload is logged once per bad file state
}

func newUserStore(path string) *userStore {
	return &userStore{path: path, users: map[string]User{}}
}

func (s *userStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// loadLocked reads the file into memory; the caller holds s.mu. The stat is
// taken before the read, so a write landing in between shows as a change on
// the next call rather than being lost. On error the last good set is kept.
func (s *userStore) loadLocked() error {
	fi, statErr := os.Stat(s.path)
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if statErr == nil {
		s.seenMod, s.seenSize, s.seenOK = fi.ModTime(), fi.Size(), true
	}
	var doc struct {
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	s.users = map[string]User{}
	for _, u := range doc.Users {
		s.users[u.Name] = u
	}
	s.warned = false
	return nil
}

// changedLocked reports whether the file on disk differs from what this store
// last read or wrote. A missing or unreadable file is not a change: the last
// good set stands.
func (s *userStore) changedLocked() bool {
	if s.path == "" {
		return false
	}
	fi, err := os.Stat(s.path)
	if err != nil {
		return false
	}
	return !s.seenOK || !fi.ModTime().Equal(s.seenMod) || fi.Size() != s.seenSize
}

// refresh re-reads users.json when something else changed it. A bad file
// keeps the last good set and is logged once.
func (s *userStore) refresh() {
	s.mu.RLock()
	changed := s.changedLocked()
	s.mu.RUnlock()
	if !changed {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.changedLocked() {
		return
	}
	if err := s.loadLocked(); err != nil {
		if !s.warned {
			log.Printf("users: keeping the last good set, cannot reload: %v", err)
			s.warned = true
		}
		// Remember this file state so a bad file is not re-read on every request.
		if fi, e := os.Stat(s.path); e == nil {
			s.seenMod, s.seenSize, s.seenOK = fi.ModTime(), fi.Size(), true
		}
	}
}

// saveLocked writes the file; the caller holds s.mu.
func (s *userStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	raw, err := json.MarshalIndent(map[string]any{"users": list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Our own write is not an outside change.
	if fi, err := os.Stat(s.path); err == nil {
		s.seenMod, s.seenSize, s.seenOK = fi.ModTime(), fi.Size(), true
	}
	return nil
}

func (s *userStore) get(name string) (User, bool) {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[name]
	return u, ok
}

func (s *userStore) anyActive() bool {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if !u.Disabled {
			return true
		}
	}
	return false
}

func (s *userStore) list() []User {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		u.Hash = ""
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// setRides replaces a user's assigned rides (de-duplicated, sorted). An empty
// list is kept as a non-nil empty list: no rides, not "legacy".
func (s *userStore) setRides(name string, rides []string) error {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return errors.New("no such user")
	}
	seen := map[string]bool{}
	list := []string{}
	for _, r := range rides {
		if r != "" && !seen[r] {
			seen[r] = true
			list = append(list, r)
		}
	}
	sort.Strings(list)
	u.Rides = list
	s.users[name] = u
	return s.saveLocked()
}

// migrateLegacyRides pins every user who has no ride list (written before
// assignment existed) to the active ride, so the deploy that introduces
// assignment locks nobody out and changes nobody's access. It returns how many
// users it pinned. Admins are left alone: they are on every ride anyway.
func (s *userStore) migrateLegacyRides(active string) (int, error) {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for name, u := range s.users {
		if u.Role != roleAdmin && u.Rides == nil && active != "" {
			u.Rides = []string{active}
			s.users[name] = u
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveLocked()
}

func (s *userStore) create(name, hash, role string) error {
	if !validUsername(name) {
		return errors.New("username: 2-32 lowercase letters, digits, - or _")
	}
	if role != roleAdmin && role != roleEditor {
		return errors.New("role must be admin or editor")
	}
	s.refresh() // never save over an outside change
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.users[name]; dup {
		return errors.New("that username exists")
	}
	s.users[name] = User{Name: name, Hash: hash, Role: role, Created: time.Now().UTC()}
	return s.saveLocked()
}

func (s *userStore) setHash(name, hash string) error {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return errors.New("no such user")
	}
	u.Hash = hash
	s.users[name] = u
	return s.saveLocked()
}

// setDisabled refuses to disable the last active admin.
func (s *userStore) setDisabled(name string, disabled bool) error {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return errors.New("no such user")
	}
	if disabled && u.Role == roleAdmin && !u.Disabled {
		others := 0
		for _, o := range s.users {
			if o.Name != name && o.Role == roleAdmin && !o.Disabled {
				others++
			}
		}
		if others == 0 {
			return errors.New("that is the last active admin")
		}
	}
	u.Disabled = disabled
	s.users[name] = u
	return s.saveLocked()
}

// LoadUsers opens $DATA_DIR/users.json. With no file it seeds user kris (admin)
// from ADMIN_PASSWORD_HASH, so the deploy that introduces users breaks nothing.
func (a *Auth) LoadUsers(dataDir string) error {
	a.users.path = filepath.Join(dataDir, usersFile)
	err := a.users.load()
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if a.seedHash == "" {
		return nil
	}
	return a.users.create("kris", a.seedHash, roleAdmin)
}

// generatePassword returns a strong random password, 20 characters from an
// alphabet with no look-alikes.
func generatePassword() (string, error) {
	const alpha = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return string(b), nil
}

// setPasswordFile is `roadtrip -set-password <user>`: read one line from r and
// set that user's password in the users file under dataDir. It is how the
// operator resets his own over `fly ssh console`.
func setPasswordFile(dataDir, user string, r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, 1024))
	if err != nil {
		return err
	}
	pw := strings.TrimRight(string(raw), "\r\n")
	if len(pw) < minPwLen {
		return fmt.Errorf("password must be at least %d characters", minPwLen)
	}
	us := newUserStore(filepath.Join(dataDir, usersFile))
	if err := us.load(); err != nil {
		return err
	}
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	return us.setHash(user, h)
}
