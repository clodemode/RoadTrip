package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
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
	// seedName is ADMIN_USERNAME, the name that first user gets ("admin").
	seedName string
	// brake slows a run of wrong passwords (signin_guard.go).
	brake *loginBrake
}

// minSecretLen is the length under which boot warns about PUSH_TOKEN or
// SESSION_KEY: each is the whole of what guards the site from a guess.
const minSecretLen = 32

func NewAuth() *Auth {
	a := &Auth{users: newUserStore(""), brake: newLoginBrake()}
	a.seedHash = os.Getenv("ADMIN_PASSWORD_HASH")
	a.seedName = strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_USERNAME")))
	if a.seedName == "" {
		a.seedName = "admin"
	} else if !validUsername(a.seedName) {
		log.Printf("WARN ADMIN_USERNAME is not a username (2 to 32 of a-z, 0-9, dash, underscore): using admin")
		a.seedName = "admin"
	}
	if t := os.Getenv("PUSH_TOKEN"); t != "" {
		a.pushToken = []byte(t)
		if len(t) < minSecretLen {
			log.Printf("WARN PUSH_TOKEN is %d characters: use at least %d random ones", len(t), minSecretLen)
		}
	}
	if k := os.Getenv("SESSION_KEY"); k != "" {
		a.signingKey = []byte(k)
		if len(k) < minSecretLen {
			log.Printf("WARN SESSION_KEY is %d characters: use at least %d random ones", len(k), minSecretLen)
		}
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
		burnCompare(password)
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)) == nil
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.signingKey)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// Issue sets the session cookie for username. The payload is
// "<exp>.<user>.<epoch>"; usernames never contain a dot (validUsername). The
// epoch is the user's own counter in the users file: signing out or a password
// change raises it, and every session issued under the old number stops
// working, on every device. A cookie from before the epoch existed has no
// third field and is read as epoch 0.
func (a *Auth) Issue(w http.ResponseWriter, secure bool, username string) {
	exp := time.Now().Add(sessionTTL).Unix()
	u, _ := a.users.get(username)
	payload := strconv.FormatInt(exp, 10) + "." + username + "." + strconv.Itoa(u.Epoch)
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
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 && len(parts) != 4 {
		return User{}, false
	}
	payload := strings.Join(parts[:len(parts)-1], ".")
	if subtle.ConstantTimeCompare([]byte(a.sign(payload)), []byte(parts[len(parts)-1])) != 1 {
		return User{}, false
	}
	epoch := 0
	if len(parts) == 4 {
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			return User{}, false
		}
		epoch = n
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return User{}, false
	}
	u, ok := a.users.get(parts[1])
	if !ok || u.Disabled || u.Epoch != epoch {
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
