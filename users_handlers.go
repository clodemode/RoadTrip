package main

import (
	"log"
	"net/http"
	"net/url"
	"strings"
)

func (a *App) usersView(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	data := map[string]any{"Title": "Users", "Users": a.auth.users.list()}
	for k, v := range extra {
		data[k] = v
	}
	w.WriteHeader(status)
	a.render(w, r, "users.html", data)
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
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": "Password must be at least 10 characters."})
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
		a.usersView(w, r, http.StatusBadRequest, map[string]any{"Error": "Password must be at least 10 characters."})
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
