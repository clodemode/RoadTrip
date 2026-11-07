package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Session auth: a small users file on the volume (users.go), an HMAC-signed
// cookie that carries the username and an expiry, and the fixed PUSH_TOKEN for
// machines. There is no self sign-up: only an admin creates a user.

const sessionCookie = "rt_session"
const sessionTTL = 30 * 24 * time.Hour

type Auth struct {
	users      *userStore
	signingKey []byte // from SESSION_KEY, or random per-boot
	// pushToken is PUSH_TOKEN: one fixed credential a machine can carry in a
	// header instead of holding a session. It exists because the cookie is a
	// person's credential — it needs a sign-in round trip and the plaintext
	// password on the wire — and the pushes (the overlay, the census, an
	// arrival) are run from curl and from a workflow that has no browser.
	// Unset is the default and means the header path does not exist at all.
	pushToken []byte
	// seedHash is ADMIN_PASSWORD_HASH: only used once, to seed user "kris"
	// when there is no users file yet.
	seedHash string
}

func NewAuth() *Auth {
	a := &Auth{users: newUserStore("")}
	a.seedHash = os.Getenv("ADMIN_PASSWORD_HASH")
	if t := os.Getenv("PUSH_TOKEN"); t != "" {
		a.pushToken = []byte(t)
	}
	if k := os.Getenv("SESSION_KEY"); k != "" {
		a.signingKey = []byte(k)
	} else {
		// No key set: generate one. Sessions then die on restart, which is the
		// safe failure mode — it logs everyone out rather than signing with a
		// predictable key.
		a.signingKey = make([]byte, 32)
		_, _ = rand.Read(a.signingKey)
	}
	return a
}

// enabled reports whether anyone can sign in at all.
func (a *Auth) enabled() bool { return a.users.anyActive() }

// Check verifies a username and password against an active user.
func (a *Auth) Check(username, password string) bool {
	u, ok := a.users.get(username)
	if !ok || u.Disabled {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)) == nil
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.signingKey)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// Issue sets the session cookie for username. The payload is "<exp>.<user>";
// usernames never contain a dot (validUsername).
func (a *Auth) Issue(w http.ResponseWriter, secure bool, username string) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := strconv.FormatInt(exp, 10) + "." + username
	val := base64.RawURLEncoding.EncodeToString([]byte(payload + "." + a.sign(payload)))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: val, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, Expires: time.Unix(exp, 0),
	})
}

func (a *Auth) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

// sessionUser returns the active user a valid cookie names.
func (a *Auth) sessionUser(r *http.Request) (User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return User{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return User{}, false
	}
	parts := strings.SplitN(string(raw), ".", 3)
	if len(parts) != 3 {
		return User{}, false
	}
	payload := parts[0] + "." + parts[1]
	if subtle.ConstantTimeCompare([]byte(a.sign(payload)), []byte(parts[2])) != 1 {
		return User{}, false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return User{}, false
	}
	u, ok := a.users.get(parts[1])
	if !ok || u.Disabled {
		return User{}, false
	}
	return u, true
}

// IsAdmin reports whether the request carries an admin's session. The push
// token is a machine credential, not a person, and is never an admin here.
func (a *Auth) IsAdmin(r *http.Request) bool {
	u, ok := a.sessionUser(r)
	return ok && u.Role == roleAdmin
}

// mayRide reports whether the request may see and write ride slug: the push
// token and an admin always, a driver only on the rides an admin assigned.
func (a *Auth) mayRide(r *http.Request, slug, active string) bool {
	if a.pushAuthed(r) {
		return true
	}
	u, ok := a.sessionUser(r)
	return ok && u.mayRide(slug, active)
}

// pushAuthed reports whether the request carries the fixed push token as
// `Authorization: Bearer <token>`. The comparison is constant-time because the
// token never changes: a per-request secret leaks nothing to a timing oracle,
// a fixed one leaks itself a byte at a time.
func (a *Auth) pushAuthed(r *http.Request) bool {
	if len(a.pushToken) == 0 {
		return false // PUSH_TOKEN unset: the header means nothing at all
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return false
	}
	got := strings.TrimSpace(h[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), a.pushToken) == 1
}

// IsAuthed is the one question both requireAuth and the whole-site visibility
// gate ask, which is why the token is answered here rather than at a route:
// putting it in this one place is what makes every admin route AND a private
// /route.geojson reachable from curl with no cookie and no sign-in.
func (a *Auth) IsAuthed(r *http.Request) bool {
	if a.pushAuthed(r) {
		return true
	}
	_, ok := a.sessionUser(r)
	return ok
}

// HashPassword is the helper behind `roadtrip -hash <password>`, so an
// operator can mint ADMIN_PASSWORD_HASH without a separate tool.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return string(h), nil
}
