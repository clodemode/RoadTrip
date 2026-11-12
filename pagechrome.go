package main

import "net/http"

// The page chrome every layout page shares: the ride's state, the menu that
// follows it, whether the Drive Tracks strip rides along, and the breadcrumb
// and title below the banner. One place, so a page cannot disagree.

type menuItem struct {
	Label, Href string
	Current     bool
	Post        bool
}

type crumb struct{ Label, Href string }

// rideActive is the ride's state, read from the store and never from dates:
// Active is a drive whose start is stamped (a real Start or the simulated car's)
// and whose finish is neither reached nor skipped. Everything else is Planning.
func rideActive(legs []Leg) bool {
	st := tripStart(legs)
	if st == nil || st.ArrivedAt == "" {
		return false
	}
	cps := rideCheckpoints(legs)
	if len(cps) == 0 {
		return false
	}
	fin := cps[len(cps)-1]
	return fin.ID != st.ID && fin.ArrivedAt == "" && fin.SkippedAt == ""
}

// rideFinished is a drive that began and whose finish is reached or skipped.
// It is the one state that is neither Active nor Planning: the banner says so,
// because "Planning" after the last check-in reads as a ride that never began.
func rideFinished(legs []Leg) bool {
	st := tripStart(legs)
	if st == nil || st.ArrivedAt == "" {
		return false
	}
	cps := rideCheckpoints(legs)
	if len(cps) == 0 {
		return false
	}
	fin := cps[len(cps)-1]
	return fin.ID != st.ID && (fin.ArrivedAt != "" || fin.SkippedAt != "")
}

// menuFor lists the masthead links in the order of the ride's state. Each link
// keeps the gate it always had; the state only decides which are worth the room.
func menuFor(active, started, driver, chesterOn, admin bool, path string) []menuItem {
	cur := func(href string) bool { return path == href }
	var m []menuItem
	add := func(label, href string, ok bool, current bool) {
		if ok {
			m = append(m, menuItem{Label: label, Href: href, Current: current, Post: label == "Post"})
		}
	}
	ride := path == "/" || len(path) > 6 && path[:6] == "/legs/"
	chester := path == "/chester" || len(path) > 9 && path[:9] == "/chester/" || len(path) > 16 && path[:16] == "/driver/chester/"
	add("Ride", "/", true, ride)
	add("Driver", "/driver", driver, cur("/driver"))
	if active {
		add("Post", "/driver/post", driver, cur("/driver/post"))
		add("Chester", "/chester", chesterOn, chester)
		add("Lodging", "/driver/lodging", driver, cur("/driver/lodging"))
		add("Photos", "/photos", true, cur("/photos"))
		add("Plan", "/plan", true, cur("/plan"))
	} else {
		// Planning: Post stays only to Start the drive.
		add("Post", "/driver/post", driver && !started, cur("/driver/post"))
		add("Plan", "/plan", true, cur("/plan"))
		add("Lodging", "/driver/lodging", driver, cur("/driver/lodging"))
		add("Photos", "/photos", true, cur("/photos"))
	}
	add("Users", "/driver/users", admin, cur("/driver/users"))
	return m
}

// pageHead is the breadcrumb and the one title. Every label is a static word,
// the ride's name or a leg's name: the same public names the page shows.
func pageHead(name string, data map[string]any, rideName string) (string, []crumb) {
	title, _ := data["Title"].(string)
	ride := crumb{rideName, "/"}
	page := func(t string) (string, []crumb) { return t, []crumb{ride, {t, ""}} }
	switch name {
	case "index.html":
		return page("Ride")
	case "leg.html":
		return title, []crumb{ride, {"Legs", ""}, {title, ""}}
	case "chester_log.html":
		return "Chester log", []crumb{ride, {"Chester", "/chester"}, {"Log", ""}}
	case "chester_memory.html":
		return "What Chester remembers about you", []crumb{ride, {"Chester", "/chester"}, {"Memory", ""}}
	case "post_form.html":
		t := "Post a Photo"
		if sm, _ := data["StartMode"].(bool); sm {
			t = "Start the drive"
		}
		if cp, ok := data["CheckinCP"].(*Checkpoint); ok && cp != nil {
			t = "Check in at " + cp.Name
		}
		return t, []crumb{ride, {"Driver", "/driver"}, {t, ""}}
	case "post_edit.html":
		return "Edit post", []crumb{ride, {"Driver", "/driver"}, {"Edit post", ""}}
	case "chester.html":
		return page("Chester")
	case "driver.html":
		return page("Driver")
	case "plan.html":
		return page("Plan")
	case "photos.html":
		return page("Photos")
	case "checklist.html":
		return page("Checklist")
	case "lodging.html":
		return page("Lodging")
	case "users.html":
		return page("Users")
	case "login.html":
		return page("Sign in")
	}
	if title == "" {
		title = "Page"
	}
	return page(title)
}

// chrome fills the template data the banner, the strip and the page head read.
func (a *App) chrome(r *http.Request, name string, data map[string]any, authed, driver bool) {
	active, started, finished := false, false, false
	if rv, err := a.loadRide(); err == nil {
		active = rideActive(rv.Legs)
		finished = rideFinished(rv.Legs)
		started = driveStarted(rv.Legs)
	}
	data["RideState"], data["RideStateLabel"] = "planning", "Planning"
	if finished {
		data["RideState"], data["RideStateLabel"] = "finished", "Finished"
	}
	if active {
		data["RideState"], data["RideStateLabel"] = "active", "Active"
		// The simulated car is the signed-in user's own business: only they see the word.
		if authed && a.simBanner(r) != "" {
			data["RideStateLabel"] = "Active, simulation"
		}
	}
	chesterOn, _ := data["ChesterOn"].(bool)
	admin, _ := data["IsAdmin"].(bool)
	data["Menu"] = menuFor(active, started, driver, chesterOn, admin, r.URL.Path)
	hud, _ := data["HUD"].(bool)
	data["TracksOn"] = a.tracks != nil
	emb, _ := data["Embed"].(bool)
	data["StripOn"] = authed && a.tracks != nil && (active || hud) && !emb
	if data["StripOn"] == true {
		data["Resume"] = a.resumeFor(r)
	}
	rideName := "Roadtrip"
	if rd, ok := data["Ride"].(*Ride); ok && rd != nil {
		rideName = rd.Name
	}
	data["PageTitle"], data["Crumbs"] = pageHead(name, data, rideName)
}
