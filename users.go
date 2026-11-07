package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	minPwLen   = 10
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
}

type userStore struct {
	mu    sync.RWMutex
	path  string // "" = memory only (tests)
	users map[string]User
}

func newUserStore(path string) *userStore {
	return &userStore{path: path, users: map[string]User{}}
}

func (s *userStore) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var doc struct {
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = map[string]User{}
	for _, u := range doc.Users {
		s.users[u.Name] = u
	}
	return nil
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
	return os.Rename(tmp, s.path)
}

func (s *userStore) get(name string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[name]
	return u, ok
}

func (s *userStore) anyActive() bool {
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

func (s *userStore) create(name, hash, role string) error {
	if !validUsername(name) {
		return errors.New("username: 2-32 lowercase letters, digits, - or _")
	}
	if role != roleAdmin && role != roleEditor {
		return errors.New("role must be admin or editor")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.users[name]; dup {
		return errors.New("that username exists")
	}
	s.users[name] = User{Name: name, Hash: hash, Role: role, Created: time.Now().UTC()}
	return s.saveLocked()
}

func (s *userStore) setHash(name, hash string) error {
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
