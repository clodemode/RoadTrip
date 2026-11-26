package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The traveller profile (spec-roadtrip-traveller-profile-holds-home-like-
// waze-a-trip-starts-or-finishes-at-home-and-a-round-trip-reads-round-trip).
//
// Each signed-in traveller has one private profile: a display name, and saved
// places of which "home" is the one reserved key. It lives in
// $DATA_DIR/profiles.json, mode 0600, written atomically the way private.json
// is, keyed by username:
//
//	{"kris": {"display_name": "Kris",
//	          "places": {"home": {"label": "Home", "address": "…", "lat": .., "lng": ..}}}}
//
// A place is private. Its exact point is kept here and nowhere else: it
// reaches the store only as a fuzzed checkpoint, snapped to the 0.01° grid
// before the trip document is even written (resolvePlaces). Its address is
// shown to its owner on their own profile page and is a navigate target for
// the signed-in driver, never on a public page or JSON, never in a log line,
// never told to Chester. Nobody can ask for another traveller's profile: the
// page has no username in its path, and the one path that names a user
// (PUT /driver/profiles/{username}) is the trip-keeper's machine path.
//
// Home's point is where the phone stands when its owner taps Set Home (Kris,
// ruled 2026-11-26). There is no third-party geocoder and no typed coordinate.

const profilesFile = "profiles.json"

// homePlace is the one reserved place key.
const homePlace = "home"

// maxHomeAccuracy is the worst phone fix, in metres, a Set Home accepts.
const maxHomeAccuracy = 100.0

type savedPlace struct {
	Label   string  `json:"label"`
	Address string  `json:"address,omitempty"`
	Lat     float64 `json:"lat,omitempty"`
	Lng     float64 `json:"lng,omitempty"`
}

// hasPoint reports whether the place has a point to put a stop on.
func (p savedPlace) hasPoint() bool { return p.Lat != 0 || p.Lng != 0 }

type profile struct {
	DisplayName string                `json:"display_name,omitempty"`
	Places      map[string]savedPlace `json:"places,omitempty"`
}

// placeKeyRE is what a saved place may be called: "home", "cottage", "work-2".
var placeKeyRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// profileStore is profiles.json behind a lock. It is read from disk on each
// call: the file is tiny and the PUT path may replace it while the app runs.
type profileStore struct {
	dir string
}

// profilesMu serialises every read-modify-write of a profiles file. One lock
// for the process: there is one data dir, and the file is tiny.
var profilesMu sync.Mutex

// profiles is the store over this app's data dir.
func (a *App) profiles() profileStore { return profileStore{dir: a.dataDir} }

func (s profileStore) path() string { return filepath.Join(s.dir, profilesFile) }

// all reads every profile. A missing file is the normal state: no profiles.
func (s profileStore) all() (map[string]profile, error) {
	out := map[string]profile{}
	raw, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// get is one traveller's profile; the zero profile when they have none.
func (s profileStore) get(user string) (profile, error) {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	all, err := s.all()
	if err != nil {
		return profile{}, err
	}
	return all[user], nil
}

// place is one saved place of one traveller.
func (s profileStore) place(user, key string) (savedPlace, bool) {
	p, err := s.get(user)
	if err != nil {
		return savedPlace{}, false
	}
	pl, ok := p.Places[key]
	return pl, ok
}

// put replaces one traveller's profile and touches no other.
func (s profileStore) put(user string, p profile) error {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	all, err := s.all()
	if err != nil {
		return err
	}
	all[user] = p
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(s.dir, profilesFile, append(raw, '\n'))
}

// writeFile0600 writes dir/name atomically with mode 0600: a temp file beside
// it, fsync, chmod, rename (the savePrivate recipe).
func writeFile0600(dir, name string, raw []byte) error {
	tmp, err := os.CreateTemp(dir, "."+strings.TrimSuffix(name, filepath.Ext(name))+"-*.json")
	if err != nil {
		return err
	}
	tname := tmp.Name()
	defer os.Remove(tname)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tname, 0o600); err != nil {
		return err
	}
	return os.Rename(tname, filepath.Join(dir, name))
}

// displayNameOf is the name a person is shown as: their profile's display
// name, else their username.
func (a *App) displayNameOf(user string) string {
	if p, err := a.profiles().get(user); err == nil && strings.TrimSpace(p.DisplayName) != "" {
		return strings.TrimSpace(p.DisplayName)
	}
	return user
}

// profileUser is the signed-in traveller a profile request is about. The push
// token is a machine, not a traveller, and has no profile of its own.
func (a *App) profileUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, ok := a.auth.sessionUser(r)
	if !ok {
		http.Error(w, "sign in as a traveller: the push token has no profile", http.StatusForbidden)
		return "", false
	}
	return u.Name, true
}

// profilePage is GET /driver/profile: the signed-in traveller's own profile.
func (a *App) profilePage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.profileUser(w, r)
	if !ok {
		return
	}
	a.renderProfile(w, r, user, http.StatusOK, "", "")
}

func (a *App) renderProfile(w http.ResponseWriter, r *http.Request, user string, status int, msg, errMsg string) {
	p, err := a.profiles().get(user)
	if err != nil {
		log.Printf("profile: could not read the profiles file")
		http.Error(w, "could not read your profile", http.StatusInternalServerError)
		return
	}
	home := p.Places[homePlace]
	// The page carries the traveller's own address: no cache may keep it. Set
	// here as well as in render, because a status other than 200 is written
	// before render runs.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	a.render(w, r, "profile.html", map[string]any{
		"Title":       "My places",
		"User":        user,
		"DisplayName": p.DisplayName,
		"Home":        home,
		"HomeSet":     home.hasPoint(),
		"MaxAccuracy": int(maxHomeAccuracy),
		"Msg":         msg,
		"Err":         errMsg,
	})
}

// profileSave is POST /driver/profile: display name, Home's address, and,
// when the phone sent one, Home's point. A point comes with its accuracy and
// is refused over maxHomeAccuracy metres. A form with no point keeps the one
// on file.
func (a *App) profileSave(w http.ResponseWriter, r *http.Request) {
	user, ok := a.profileUser(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	p, err := a.profiles().get(user)
	if err != nil {
		http.Error(w, "could not read your profile", http.StatusInternalServerError)
		return
	}
	if p.Places == nil {
		p.Places = map[string]savedPlace{}
	}
	home := p.Places[homePlace]
	home.Label = "Home"
	p.DisplayName = strings.TrimSpace(r.PostFormValue("display_name"))
	if len(p.DisplayName) > 64 {
		a.renderProfile(w, r, user, http.StatusBadRequest, "", "A display name is at most 64 characters.")
		return
	}
	home.Address = strings.TrimSpace(r.PostFormValue("home_address"))
	if len(home.Address) > 200 {
		a.renderProfile(w, r, user, http.StatusBadRequest, "", "An address is at most 200 characters.")
		return
	}
	if lat, lng := r.PostFormValue("home_lat"), r.PostFormValue("home_lng"); lat != "" || lng != "" {
		la, err1 := strconv.ParseFloat(lat, 64)
		ln, err2 := strconv.ParseFloat(lng, 64)
		acc, err3 := strconv.ParseFloat(r.PostFormValue("home_accuracy"), 64)
		switch {
		case err1 != nil || err2 != nil || math.Abs(la) > 90 || math.Abs(ln) > 180:
			a.renderProfile(w, r, user, http.StatusBadRequest, "", "The phone sent no usable position.")
			return
		case err3 != nil || acc <= 0 || acc > maxHomeAccuracy:
			a.renderProfile(w, r, user, http.StatusBadRequest, "",
				fmt.Sprintf("The phone's fix is not close enough: Home needs one within %d m. Step outside and try again.", int(maxHomeAccuracy)))
			return
		}
		home.Lat, home.Lng = la, ln
	}
	if home.Address == "" && !home.hasPoint() {
		delete(p.Places, homePlace)
	} else {
		p.Places[homePlace] = home
	}
	if err := a.profiles().put(user, p); err != nil {
		log.Printf("profile: could not save %s's profile", user)
		http.Error(w, "could not save your profile", http.StatusInternalServerError)
		return
	}
	// The user and nothing else: never a place, never a point.
	log.Printf("profile: %s saved their profile", user)
	a.renderProfile(w, r, user, http.StatusOK, "Saved.", "")
}

// profilePut is PUT /driver/profiles/{username}: one traveller's whole block,
// as JSON, from the trip-keeper (an admin session or the push token). It is
// the machine path the persistence copy is restored through.
func (a *App) profilePut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("username")
	if !validUsername(name) {
		http.Error(w, "not a username", http.StatusBadRequest)
		return
	}
	if _, ok := a.auth.users.get(name); !ok {
		http.Error(w, "no user "+strconv.Quote(name), http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read the body (limit 64 KB)", http.StatusBadRequest)
		return
	}
	var p profile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		http.Error(w, "not a profile: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := checkProfile(p); err != nil {
		http.Error(w, "not a profile: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.profiles().put(name, p); err != nil {
		log.Printf("profile: could not save %s's profile", name)
		http.Error(w, "could not save the profile", http.StatusInternalServerError)
		return
	}
	keys := make([]string, 0, len(p.Places))
	for k := range p.Places {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	log.Printf("profile: %s's profile replaced (%d place(s))", name, len(keys))
	// The place keys, never an address or a point.
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": name, "places": keys})
}

// checkProfile refuses a block a page could not have written.
func checkProfile(p profile) error {
	if len(p.DisplayName) > 64 {
		return errors.New("display_name is longer than 64 characters")
	}
	for k, pl := range p.Places {
		if !placeKeyRE.MatchString(k) {
			return fmt.Errorf("place key %q must be lowercase letters, digits and dashes", k)
		}
		if math.Abs(pl.Lat) > 90 || math.Abs(pl.Lng) > 180 {
			return fmt.Errorf("place %q has a point off the globe", k)
		}
		if len(pl.Address) > 200 {
			return fmt.Errorf("place %q has an address longer than 200 characters", k)
		}
	}
	return nil
}
