package main

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// What stands between the open sign-in form and the session (audit,
// 2026-11-16): where a sign-in may send the browser afterwards, a brake on a
// run of wrong passwords, and a compare that takes as long for a name nobody
// has as for one somebody does.

// safeNext is the address a sign-in may redirect to: a path on this site, or
// "/driver". A browser reads a backslash as a slash and drops a tab or a
// newline, so "/\host" and "/<tab>/host" leave the site although they begin
// with one slash; both are refused, with anything that parses to a scheme or
// a host.
func safeNext(next string) string {
	const home = "/driver"
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return home
	}
	for _, c := range next {
		if c == '\\' || c < 0x20 || c == 0x7f {
			return home
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return home
	}
	return next
}

const (
	loginWindow = 15 * time.Minute
	// Wrong passwords one address may send in a window before it is refused.
	loginAddrLimit = 5
	// Wrong passwords one username may draw in a window, from every address
	// together. It is far looser than the per-address limit on purpose: this
	// one can be spent by a stranger who knows a name, and the trip-keeper
	// signs in from a car.
	loginUserLimit = 20
	loginKeysMax   = 4096
)

// loginBrake counts wrong passwords per address and per username in a fixed
// window, in memory. A restart forgets it, which is the cheap failure. The
// address is clientAddr's (Fly-Client-IP behind Fly's edge): keyed on
// RemoteAddr alone every request would be the proxy.
type loginBrake struct {
	mu  sync.Mutex
	n   map[string]int
	end map[string]time.Time
	now func() time.Time
}

func newLoginBrake() *loginBrake {
	return &loginBrake{n: map[string]int{}, end: map[string]time.Time{}, now: time.Now}
}

func loginKeys(addr, username string) (string, string) {
	if len(username) > 64 {
		username = username[:64]
	}
	return "a:" + addr, "u:" + username
}

func (b *loginBrake) count(key string, now time.Time) int {
	if !now.Before(b.end[key]) {
		delete(b.n, key)
		delete(b.end, key)
	}
	return b.n[key]
}

// refused reports whether a sign-in from addr for username is over a limit,
// and for how long.
func (b *loginBrake) refused(addr, username string) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	ka, ku := loginKeys(addr, username)
	if b.count(ka, now) >= loginAddrLimit {
		return true, b.end[ka].Sub(now)
	}
	if b.count(ku, now) >= loginUserLimit {
		return true, b.end[ku].Sub(now)
	}
	return false, 0
}

// failed records one wrong password.
func (b *loginBrake) failed(addr, username string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if len(b.n) >= loginKeysMax {
		for k := range b.n {
			b.count(k, now)
		}
		if len(b.n) >= loginKeysMax { // still full of live windows: start over
			b.n, b.end = map[string]int{}, map[string]time.Time{}
		}
	}
	ka, ku := loginKeys(addr, username)
	for _, k := range []string{ka, ku} {
		if b.count(k, now) == 0 {
			b.end[k] = now.Add(loginWindow)
		}
		b.n[k]++
	}
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// burnCompare spends one bcrypt compare on a name that has no user, so the
// time a wrong sign-in takes does not say which names exist.
func burnCompare(password string) {
	dummyHashOnce.Do(func() {
		h, err := HashPassword("no user has this password")
		if err == nil {
			dummyHash = []byte(h)
		}
	})
	if dummyHash != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
	}
}
