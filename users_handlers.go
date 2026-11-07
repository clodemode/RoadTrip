package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

func (a *App) usersView(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	type userRow struct {
		User
		Rides []rideBox
		All   bool // an admin: on every ride, nothing to tick
	}
	var rides []RideCensus
	if rs, err := a.store.Rides(); err == nil {
		rides = rs
	}
	active := a.activeSlug()
	var rows []userRow
	for _, u := range a.auth.users.list() {
		row := userRow{User: u, All: u.Role == roleAdmin}
		for _, c := range rides {
			row.Rides = append(row.Rides, rideBox{Slug: c.Slug, Name: c.Name, On: u.mayRide(c.Slug, active), Active: c.Slug == active})
		}
		rows = append(rows, row)
	}
	data := map[string]any{"Title": "Users", "Users": rows, "RideChoices": rideBoxes(rides, active)}
	for k, v := range extra {
		data[k] = v
	}
	w.WriteHeader(status)
	a.render(w, r, "users.html", data)
}

type rideBox struct {
	Slug, Name string
	On, Active bool
}

func rideBoxes(rides []RideCensus, active string) []rideBox {
	var out []rideBox
	for _, c := range rides {
		out = append(out, rideBox{Slug: c.Slug, Name: c.Name, On: c.Slug == active, Active: c.Slug == active})
	}
	return out
}

// usersRides sets which rides a driver is assigned to: the form's ride values,
// each of which must be a ride in the database. None ticked means no rides.
func (a *App) usersRides(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	if _, ok := a.auth.users.get(name); !ok {
		http.NotFound(w, r)
		return
	}
	for _, slug := range r.PostForm["ride"] {
		if _, err := a.store.Ride(slug); err != nil {
			a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": "No ride " + slug + " in the database."})
			return
		}
	}
	if err := a.auth.users.setRides(name, r.PostForm["ride"]); err != nil {
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	log.Printf("user rides: %s now on %v", name, r.PostForm["ride"])
	http.Redirect(w, r, "/driver/users", http.StatusFound)
}

func (a *App) usersPage(w http.ResponseWriter, r *http.Request) {
	a.usersView(w, r, http.StatusOK, map[string]any{"Error": r.URL.Query().Get("error")})
}

// usersCreate adds a user. With generate=1 the server makes the password and
// shows it on this one response; it is never stored in the clear or shown again.
func (a *App) usersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	role := r.PostFormValue("role")
	if role == "" {
		role = roleEditor
	}
	pw := r.PostFormValue("password")
	generated := false
	if r.PostFormValue("generate") != "" && pw == "" {
		var err error
		if pw, err = generatePassword(); err != nil {
			http.Error(w, "could not generate", 500)
			return
		}
		generated = true
	}
	if len(pw) < minPwLen {
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": fmt.Sprintf("Password must be at least %d characters.", minPwLen)})
		return
	}
	h, err := HashPassword(pw)
	if err == nil {
		err = a.auth.users.create(name, h, role)
	}
	if err != nil {
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	if role != roleAdmin {
		if err := a.auth.users.setRides(name, r.PostForm["ride"]); err != nil {
			log.Printf("user %s created but rides not set: %v", name, err)
		}
	}
	log.Printf("user created: %s (%s)", name, role)
	if generated {
		a.usersView(w, r, http.StatusOK, map[string]any{"ShownFor": name, "ShownPassword": pw})
		return
	}
	http.Redirect(w, r, "/driver/users", http.StatusFound)
}

func (a *App) usersReset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	name := r.PathValue("name")
	if _, ok := a.auth.users.get(name); !ok {
		http.NotFound(w, r)
		return
	}
	pw := r.PostFormValue("password")
	generated := false
	if pw == "" && r.PostFormValue("generate") != "" {
		pw, _ = generatePassword()
		generated = true
	}
	if len(pw) < minPwLen {
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": fmt.Sprintf("Password must be at least %d characters.", minPwLen)})
		return
	}
	h, err := HashPassword(pw)
	if err == nil {
		err = a.auth.users.setHash(name, h)
	}
	if err != nil {
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	log.Printf("user password reset: %s", name)
	if generated {
		a.usersView(w, r, http.StatusOK, map[string]any{"ShownFor": name, "ShownPassword": pw})
		return
	}
	http.Redirect(w, r, "/driver/users", http.StatusFound)
}

func (a *App) usersSetDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	if err := a.auth.users.setDisabled(r.PathValue("name"), disabled); err != nil {
		http.Redirect(w, r, "/driver/users?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/driver/users", http.StatusFound)
}

func (a *App) usersDisable(w http.ResponseWriter, r *http.Request) { a.usersSetDisabled(w, r, true) }
func (a *App) usersEnable(w http.ResponseWriter, r *http.Request)  { a.usersSetDisabled(w, r, false) }
