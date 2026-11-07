package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// asUser adds an active user to the app and returns the cookie a browser
// would carry after signing in as them.
func asUser(t *testing.T, app *App, name, role string) *http.Cookie {
	t.Helper()
	if _, ok := app.auth.users.get(name); !ok {
		h, err := HashPassword("pw-" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.auth.users.create(name, h, role); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	rec := httptest.NewRecorder()
	app.auth.Issue(rec, false, name)
	return rec.Result().Cookies()[0]
}

func doAs(t *testing.T, app *App, c *http.Cookie, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	return w
}

func TestSeedsKrisFromAdminPasswordHash(t *testing.T) {
	dir := t.TempDir()
	h, _ := HashPassword("old-secret")
	t.Setenv("ADMIN_PASSWORD_HASH", h)
	a := NewAuth()
	if err := a.LoadUsers(dir); err != nil {
		t.Fatal(err)
	}
	u, ok := a.users.get("kris")
	if !ok || u.Role != roleAdmin || u.Disabled {
		t.Fatalf("kris not seeded as admin: %+v ok=%v", u, ok)
	}
	if !a.Check("kris", "old-secret") || a.Check("kris", "nope") {
		t.Fatal("seeded password does not verify")
	}
	// A second boot reads the file; a changed env hash no longer overrides it.
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	b := NewAuth()
	if err := b.LoadUsers(dir); err != nil {
		t.Fatal(err)
	}
	if !b.Check("kris", "old-secret") {
		t.Fatal("users file did not persist")
	}
	if fi, err := os.Stat(filepath.Join(dir, "users.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("users.json: %v %v", fi, err)
	}
}

func TestNoUsersNoSeedMeansReadOnly(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	a := NewAuth()
	if err := a.LoadUsers(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if a.enabled() {
		t.Fatal("sign-in must be disabled with no users")
	}
}

func TestLoginWithUsernameAndPassword(t *testing.T) {
	app := testApp(t)
	asUser(t, app, "pat", roleEditor)
	w := doAs(t, app, nil, "POST", "/driver/login", url.Values{"username": {"pat"}, "password": {"pw-pat"}})
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/driver" {
		t.Fatalf("login: %d %q, want 302 /driver", w.Code, w.Header().Get("Location"))
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("no cookie")
	}
	for _, bad := range []url.Values{
		{"username": {"pat"}, "password": {"wrong"}},
		{"username": {"ghost"}, "password": {"pw-pat"}},
		{"password": {"pw-pat"}},
	} {
		if w := doAs(t, app, nil, "POST", "/driver/login", bad); w.Code != http.StatusUnauthorized {
			t.Errorf("bad login %v: %d, want 401", bad, w.Code)
		}
	}
}

func TestOldAdminPathsAre404(t *testing.T) {
	app := testApp(t)
	c := asUser(t, app, "kris", roleAdmin)
	for _, p := range []string{"/admin", "/admin/login", "/admin/post", "/admin/posts.json", "/admin/checklist.json"} {
		if w := doAs(t, app, c, "GET", p, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, w.Code)
		}
	}
	if w := doAs(t, app, c, "GET", "/driver/posts.json", nil); w.Code != http.StatusOK {
		t.Errorf("/driver/posts.json = %d", w.Code)
	}
	if w := doAs(t, app, c, "GET", "/driver", nil); w.Code != http.StatusFound {
		t.Errorf("/driver = %d, want a redirect to the post screen", w.Code)
	}
}

func TestDisabledUserSessionDies(t *testing.T) {
	app := testApp(t)
	admin := asUser(t, app, "kris", roleAdmin)
	pat := asUser(t, app, "pat", roleEditor)
	if w := doAs(t, app, pat, "GET", "/driver/posts.json", nil); w.Code != 200 {
		t.Fatalf("editor posts.json = %d", w.Code)
	}
	doAs(t, app, admin, "POST", "/driver/users/pat/disable", url.Values{})
	if w := doAs(t, app, pat, "GET", "/driver/posts.json", nil); w.Code == 200 {
		t.Fatal("disabled user still signed in")
	}
	if w := doAs(t, app, nil, "POST", "/driver/login", url.Values{"username": {"pat"}, "password": {"pw-pat"}}); w.Code != 401 {
		t.Fatalf("disabled user login = %d", w.Code)
	}
}

func TestOnlyAdminsManageUsers(t *testing.T) {
	app := testApp(t)
	admin := asUser(t, app, "kris", roleAdmin)
	editor := asUser(t, app, "pat", roleEditor)

	// Anonymous: no self sign-up, no page.
	if w := doAs(t, app, nil, "POST", "/driver/users", url.Values{"username": {"eve"}, "password": {"long-enough-pw-1"}}); w.Code == 200 || w.Code == 302 && !strings.Contains(w.Header().Get("Location"), "login") {
		t.Errorf("anonymous create = %d", w.Code)
	}
	if _, ok := app.auth.users.get("eve"); ok {
		t.Fatal("anonymous created a user")
	}
	if w := doAs(t, app, nil, "GET", "/driver/signup", nil); w.Code != 404 {
		t.Errorf("signup = %d, want 404", w.Code)
	}
	// Editor: signed in, but not an admin.
	if w := doAs(t, app, editor, "GET", "/driver/users", nil); w.Code != http.StatusForbidden {
		t.Errorf("editor GET users = %d, want 403", w.Code)
	}
	if w := doAs(t, app, editor, "POST", "/driver/users", url.Values{"username": {"eve"}, "password": {"long-enough-pw-1"}}); w.Code != http.StatusForbidden {
		t.Errorf("editor POST users = %d, want 403", w.Code)
	}
	// The push token is a machine, never an admin.
	t.Setenv("PUSH_TOKEN", "tok")
	app2 := testApp(t)
	if w := send(t, app2, "GET", "/driver/users", "tok", "", nil); w.Code != http.StatusForbidden {
		t.Errorf("push token GET users = %d, want 403", w.Code)
	}
	// Editors can do the editor work.
	if w := doAs(t, app, editor, "GET", "/driver/post", nil); w.Code != 200 {
		t.Errorf("editor post form = %d", w.Code)
	}
	// Admin lists and adds.
	w := doAs(t, app, admin, "GET", "/driver/users", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "pat") {
		t.Fatalf("admin users page = %d", w.Code)
	}
	w = doAs(t, app, admin, "POST", "/driver/users", url.Values{"username": {"eve"}, "password": {"long-enough-pw-1"}, "role": {"editor"}})
	if w.Code != http.StatusFound && w.Code != 200 {
		t.Fatalf("admin create = %d", w.Code)
	}
	u, ok := app.auth.users.get("eve")
	if !ok || u.Role != roleEditor || bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte("long-enough-pw-1")) != nil {
		t.Fatalf("eve not created right: %+v", u)
	}
	// Bad names and weak passwords are refused.
	for _, f := range []url.Values{
		{"username": {"a.b"}, "password": {"long-enough-pw-1"}},
		{"username": {"Bad Name"}, "password": {"long-enough-pw-1"}},
		{"username": {"short"}, "password": {"x"}},
		{"username": {"eve"}, "password": {"long-enough-pw-1"}}, // duplicate
	} {
		doAs(t, app, admin, "POST", "/driver/users", f)
	}
	if n := len(app.auth.users.list()); n != 3 {
		t.Errorf("users = %d, want 3 (kris, pat, eve)", n)
	}
}

func TestGeneratedAndResetPasswordsShownOnce(t *testing.T) {
	app := testApp(t)
	admin := asUser(t, app, "kris", roleAdmin)
	w := doAs(t, app, admin, "POST", "/driver/users", url.Values{"username": {"sam"}, "generate": {"1"}})
	if w.Code != 200 {
		t.Fatalf("generate = %d (the password is shown on this response, no redirect)", w.Code)
	}
	u, _ := app.auth.users.get("sam")
	body := w.Body.String()
	// Find a token in the page that verifies as sam's password.
	found := ""
	for _, f := range strings.FieldsFunc(body, func(r rune) bool { return r == '<' || r == '>' || r == ' ' || r == '\n' }) {
		if len(f) >= 16 && bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(f)) == nil {
			found = f
		}
	}
	if found == "" {
		t.Fatal("generated password not shown once")
	}
	if g := doAs(t, app, admin, "GET", "/driver/users", nil); strings.Contains(g.Body.String(), found) {
		t.Fatal("password shown again on the list")
	}
	// Reset.
	r := doAs(t, app, admin, "POST", "/driver/users/sam/reset", url.Values{"password": {"another-long-pw-2"}})
	if r.Code != 302 && r.Code != 200 {
		t.Fatalf("reset = %d", r.Code)
	}
	if !app.auth.Check("sam", "another-long-pw-2") || app.auth.Check("sam", found) {
		t.Fatal("reset did not take")
	}
}

func TestSetPasswordCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	a := NewAuth()
	if err := a.LoadUsers(dir); err != nil {
		t.Fatal(err)
	}
	h, _ := HashPassword("x")
	a.users.create("kris", h, roleAdmin)
	if err := setPasswordFile(dir, "kris", strings.NewReader("brand-new-password\n")); err != nil {
		t.Fatal(err)
	}
	b := NewAuth()
	b.LoadUsers(dir)
	if !b.Check("kris", "brand-new-password") {
		t.Fatal("password not set")
	}
	if err := setPasswordFile(dir, "ghost", strings.NewReader("brand-new-password\n")); err == nil {
		t.Fatal("setting a ghost's password must fail")
	}
	if err := setPasswordFile(dir, "kris", strings.NewReader("\n")); err == nil {
		t.Fatal("empty password must fail")
	}
}

func TestMinimumPasswordIsSixCharacters(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	a := NewAuth()
	if err := a.LoadUsers(dir); err != nil {
		t.Fatal(err)
	}
	h, _ := HashPassword("x")
	a.users.create("kris", h, roleAdmin)
	if err := setPasswordFile(dir, "kris", strings.NewReader("abcde\n")); err == nil {
		t.Fatal("5 characters must be refused")
	}
	// Length is the only rule: no digits, symbols or mixed case required.
	if err := setPasswordFile(dir, "kris", strings.NewReader("abcdef\n")); err != nil {
		t.Fatalf("6 plain lowercase characters must be accepted: %v", err)
	}
	// The users page applies the same rule.
	app := testApp(t)
	c := asUser(t, app, "root", roleAdmin)
	w := doAs(t, app, c, "POST", "/driver/users", url.Values{"username": {"sam"}, "password": {"abcde"}, "role": {roleEditor}})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "at least 6 characters") {
		t.Fatalf("5-char password on the users page: %d", w.Code)
	}
	w = doAs(t, app, c, "POST", "/driver/users", url.Values{"username": {"sam"}, "password": {"abcdef"}, "role": {roleEditor}})
	if w.Code != http.StatusFound {
		t.Fatalf("6-char password on the users page: %d", w.Code)
	}
}

func TestPasswordSetOutsideTheServerNeedsNoRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	a := NewAuth()
	if err := a.LoadUsers(dir); err != nil {
		t.Fatal(err)
	}
	h, _ := HashPassword("old-password")
	if err := a.users.create("kris", h, roleAdmin); err != nil {
		t.Fatal(err)
	}
	if !a.Check("kris", "old-password") {
		t.Fatal("old password should work")
	}
	// What `roadtrip -set-password` does from another process.
	if err := setPasswordFile(dir, "kris", strings.NewReader("newpass\n")); err != nil {
		t.Fatal(err)
	}
	if !a.Check("kris", "newpass") {
		t.Fatal("running store did not pick up the changed users.json")
	}
	if a.Check("kris", "old-password") {
		t.Fatal("old password still works")
	}
	// A server write must not be mistaken for an outside change.
	if err := a.users.create("ann", h, roleEditor); err != nil {
		t.Fatal(err)
	}
	a.users.mu.RLock()
	stale := a.users.changedLocked()
	a.users.mu.RUnlock()
	if stale {
		t.Fatal("the store's own write looks like an outside change")
	}
	if _, ok := a.users.get("ann"); !ok {
		t.Fatal("own write lost")
	}
}

func TestBadUsersFileKeepsLastGoodSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	a := NewAuth()
	a.LoadUsers(dir)
	h, _ := HashPassword("old-password")
	a.users.create("kris", h, roleAdmin)
	if err := os.WriteFile(filepath.Join(dir, usersFile), []byte("{not json, and longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.Check("kris", "old-password") {
		t.Fatal("a corrupt file dropped the last good users")
	}
}

func TestSignedInPostMustBeOwnOrigin(t *testing.T) {
	app := testApp(t)
	admin := asUser(t, app, "kris", roleAdmin)
	req := httptest.NewRequest("POST", "/driver/users", strings.NewReader("username=zed&generate=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(admin)
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin = %d", w.Code)
	}
}

// testAdminUser makes sure an admin named kris exists and returns the name.
func testAdminUser(t *testing.T, app *App) string {
	t.Helper()
	asUser(t, app, "kris", roleAdmin)
	return "kris"
}

func TestChecklistTapBoxesAreForSignedInOnly(t *testing.T) {
	app := checklistApp(t)
	markOK(t, app, clStep, "call-ahead", url.Values{})
	anon := doAs(t, app, nil, "GET", "/checklist", nil).Body.String()
	if strings.Contains(anon, "cl-check") || strings.Contains(anon, "checklist.js") {
		t.Fatal("a visitor must see no checkbox and no script")
	}
	if !strings.Contains(anon, "Done") {
		t.Fatal("a visitor still sees the read-only state")
	}
	c := asUser(t, app, "pat", roleEditor)
	in := doAs(t, app, c, "GET", "/checklist", nil).Body.String()
	if !strings.Contains(in, `data-kind="step" data-id="call-ahead"`) || !strings.Contains(in, "checklist.js") {
		t.Fatal("signed-in page has no tap box for a step")
	}
	if !strings.Contains(in, `data-id="call-ahead" checked`) {
		t.Fatal("a done step's box is not checked")
	}
	if strings.Contains(in, `data-kind="question"`) {
		t.Fatal("questions need an answer, not a tap")
	}
	// The tap is a fetch to the mark route, answered in JSON.
	req := httptest.NewRequest("POST", "/driver/checklist/step/deposit", strings.NewReader(""))
	req.Header.Set("Accept", "application/json")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"done": true`) && !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatalf("tap = %d %s", w.Code, w.Body.String())
	}
	if w := doAs(t, app, nil, "GET", "/static/js/checklist.js", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "/driver/checklist/") {
		t.Fatalf("checklist.js = %d", w.Code)
	}
}
