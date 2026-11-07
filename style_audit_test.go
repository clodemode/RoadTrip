package main

import (
	"crypto/sha512"
	"encoding/base64"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The style audit: the small checks that keep the design-system restyle true.
// One light theme, no browser dialog, no asset from another host than the map
// tiles, Bootstrap vendored byte for byte, and a foundation stylesheet that
// says which fold it is. Every walk refuses to pass on an empty input: a check
// that read nothing has checked nothing.

// shipped returns every embedded file under root whose name ends in one of
// the suffixes, read from the FS the binary actually serves.
func shipped(t *testing.T, fsys fs.FS, root string, suffixes ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		for _, s := range suffixes {
			if strings.HasSuffix(path, s) {
				b, err := fs.ReadFile(fsys, path)
				if err != nil {
					return err
				}
				out[path] = string(b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func requireFiles(t *testing.T, got map[string]string, want ...string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatal("the walk found no files: an empty walk proves nothing")
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Fatalf("the walk did not reach %s (it found %d file(s))", w, len(got))
		}
	}
}

// ourPages returns our templates, our stylesheets and our scripts (the
// vendored files are left out: they are checked byte for byte or not ours).
func ourPages(t *testing.T) map[string]string {
	t.Helper()
	files := shipped(t, templateFS, "templates", ".html")
	requireFiles(t, files, "templates/base.html", "templates/index.html", "templates/leg.html")
	css := shipped(t, staticFS, "static/css", ".css")
	requireFiles(t, css, "static/css/foundation.css", "static/css/app.css")
	js := shipped(t, staticFS, "static/js", ".js")
	requireFiles(t, js, "static/js/map.js", "static/js/plan.js", "static/js/post.js", "static/js/chester.js")
	for _, m := range []map[string]string{css, js} {
		for p, b := range m {
			files[p] = b
		}
	}
	return files
}

// One light theme: nothing of ours asks the browser for a dark one, whether by
// a media query (in a stylesheet, an inline style or a script's matchMedia), a
// color-scheme that admits dark, or a dark Bootstrap theme.
var darkTheme = regexp.MustCompile(`(?i)prefers-color-scheme|color-scheme[^;>{}]*\bdark\b|data-bs-theme\s*=\s*["']?\s*dark|setAttribute\(\s*["']data-bs-theme`)

func TestStyleNoColourSchemeMediaQuery(t *testing.T) {
	for path, body := range ourPages(t) {
		if m := darkTheme.FindString(body); m != "" {
			t.Errorf("%s asks for a dark theme (%s): the site has one light theme", path, m)
		}
	}
	// Positive control: the pattern reads each way a dark theme gets in.
	for _, s := range []string{"@media (prefers-color-scheme: dark)", `<meta name="color-scheme" content="light dark">`, "color-scheme: dark;", `<html data-bs-theme="dark">`} {
		if !darkTheme.MatchString(s) {
			t.Fatalf("the dark-theme pattern misses %q: the check cannot fire", s)
		}
	}
	if darkTheme.MatchString("color-scheme: light;") {
		t.Fatal("the dark-theme pattern refuses the light declaration foundation.css makes")
	}
}

// No modal and no browser dialog: content opens in place instead.
var dialogCall = regexp.MustCompile(`(?i)\b(confirm|alert|prompt)\s*\(|\[\s*["'](confirm|alert|prompt)["']\s*\]|<dialog\b|\.showModal\s*\(|class\s*=\s*["'][^"']*\bmodal\b|data-bs-toggle\s*=\s*["']?modal`)

func TestStyleNoBrowserDialogs(t *testing.T) {
	files := ourPages(t)
	for path, body := range files {
		if strings.HasSuffix(path, ".css") {
			continue // a stylesheet cannot open a dialog, and its comments may name one
		}
		if m := dialogCall.FindString(body); m != "" {
			t.Errorf("%s opens a dialog or a modal (%s): open it in place with a <details> instead", path, m)
		}
	}
	for _, s := range []string{"return confirm('x')", "window.confirm ('x')", "window['alert']('x')", "<dialog open>", "d.showModal()", `<div class="modal fade">`, `data-bs-toggle="modal"`} {
		if !dialogCall.MatchString(s) {
			t.Fatalf("the dialog pattern misses %q: the check cannot fire", s)
		}
	}
}

// The "Whole trip" button on /plan is hidden by its hidden attribute until a
// day is zoomed, so the attribute must beat every display rule a button gets.
func TestStyleHiddenAttributeAlwaysWins(t *testing.T) {
	b, err := staticFS.ReadFile("static/css/foundation.css")
	if err != nil {
		t.Fatalf("foundation.css is not embedded: %v", err)
	}
	if !regexp.MustCompile(`\[hidden\]\s*\{\s*display\s*:\s*none\s*!important\s*;?\s*\}`).Match(b) {
		t.Error("foundation.css does not carry [hidden] { display: none !important; }")
	}
	plan, err := templateFS.ReadFile("templates/plan.html")
	if err != nil {
		t.Fatalf("plan.html: %v", err)
	}
	if !regexp.MustCompile(`<button\b[^>]*\bid="plan-all"[^>]*\bhidden\b`).Match(plan) {
		t.Error(`plan.html: the button with id plan-all no longer starts hidden`)
	}
}

// The map tiles are the site's only third-party request. Every asset a page
// loads (a src, a stylesheet link, a CSS url() or @import, a script's fetch
// or import, an absolute URL in our own scripts) is on this host or is the
// tile host. Plain <a href> links to other sites are not assets.
const tileHost = "tile.openstreetmap.org"

func absHost(u string) (string, bool) {
	// Browsers read the scheme in any case and a backslash as a slash.
	u = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(u), `\`, "/"))
	switch {
	case strings.HasPrefix(u, "https://"):
		u = u[len("https://"):]
	case strings.HasPrefix(u, "http://"):
		u = u[len("http://"):]
	case strings.HasPrefix(u, "//"):
		u = u[2:]
	default:
		return "", false
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, ":"); i >= 0 {
		u = u[:i]
	}
	return strings.ToLower(u), true
}

func allowedAssetHost(host string) bool {
	return host == tileHost || strings.HasSuffix(host, "."+tileHost)
}

var (
	tmplAsset = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:src|poster|data|action)\s*=\s*["']?\s*([^"'\s>]+)`),
	}
	// srcset holds a list: every candidate is an asset, not just the first.
	srcsetAttr = regexp.MustCompile(`(?i)\bsrcset\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	// href loads an asset on every element but a link someone follows: <link>,
	// and SVG <image> and <use>, all count; <a> and <area> do not.
	hrefAttr = regexp.MustCompile(`(?i)<([a-z][a-z0-9:-]*)\b[^>]*?\b(?:xlink:)?href\s*=\s*["']?\s*([^"'\s>]+)`)
	cssAsset = []*regexp.Regexp{
		regexp.MustCompile(`(?i)url\(\s*["']?\s*([^"')\s]+)`),
		regexp.MustCompile(`(?i)@import\s+(?:url\(\s*)?["']?\s*([^"')\s;]+)`),
	}
	jsLoad     = regexp.MustCompile(`(?i)\b(?:fetch|import|importScripts|open|sendBeacon|EventSource|WebSocket)\s*\(\s*(?:["'][A-Z]+["']\s*,\s*)?["'\x60]([^"'\x60\s]+)`)
	jsAbsolute = regexp.MustCompile("[\"'\x60]((?:https?:)?//[^\"'\x60\\s]+)")
)

// assetURLs runs the patterns over one file and returns every URL they caught,
// local or not, so the caller can prove the patterns fired at all.
func assetURLs(body string, pats ...*regexp.Regexp) []string {
	var out []string
	for _, p := range pats {
		for _, m := range p.FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// templateAssetURLs is assetURLs for markup: the attribute patterns, every
// srcset candidate, and every href that is not on a followed link.
func templateAssetURLs(body string) []string {
	out := assetURLs(body, tmplAsset...)
	for _, m := range srcsetAttr.FindAllStringSubmatch(body, -1) {
		for _, cand := range strings.Split(m[1]+m[2]+m[3], ",") {
			if f := strings.Fields(cand); len(f) > 0 {
				out = append(out, f[0])
			}
		}
	}
	for _, m := range hrefAttr.FindAllStringSubmatch(body, -1) {
		if tag := strings.ToLower(m[1]); tag != "a" && tag != "area" {
			out = append(out, m[2])
		}
	}
	return out
}

func TestStyleNoThirdPartyAssetsCatchesEveryLoad(t *testing.T) {
	for _, s := range []string{
		`<link rel="stylesheet" href="HTTPS://cdn.example.com/x.css">`,
		`<img srcset="/a.jpg 1x, https://cdn.example.com/b.jpg 2x">`,
		`<svg><image href="https://cdn.example.com/i.png"/></svg>`,
		`<svg><use xlink:href="//cdn.example.com/s.svg#i"/></svg>`,
		`<script src="\\cdn.example.com/x.js"></script>`,
		`<link rel="preload" href="https://cdn.example.com/f.woff2">`,
	} {
		bad := false
		for _, u := range templateAssetURLs(s) {
			if h, abs := absHost(u); abs && !allowedAssetHost(h) {
				bad = true
			}
		}
		if !bad {
			t.Errorf("the asset patterns let %s through", s)
		}
	}
	for _, s := range []string{`<a href="https://github.com/clodemode">Source</a>`, `<link rel="stylesheet" href="/static/css/app.css">`} {
		for _, u := range templateAssetURLs(s) {
			if h, abs := absHost(u); abs && !allowedAssetHost(h) {
				t.Errorf("the asset patterns refuse %s, which loads nothing from another host", s)
			}
		}
	}
}

func TestStyleNoThirdPartyAssets(t *testing.T) {
	checked := map[string]int{}
	check := func(kind, path string, urls []string) {
		for _, u := range urls {
			checked[kind]++
			if host, abs := absHost(u); abs && !allowedAssetHost(host) {
				t.Errorf("%s loads an asset from %s (%s): only the map tiles may come from another host", path, host, u)
			}
		}
	}

	tmpl := shipped(t, templateFS, "templates", ".html")
	requireFiles(t, tmpl, "templates/base.html")
	for path, body := range tmpl {
		check("template", path, templateAssetURLs(body))
		check("template", path, assetURLs(body, cssAsset...)) // inline style blocks and style= attributes
		check("template", path, assetURLs(body, jsLoad))      // inline scripts
	}

	css := shipped(t, staticFS, "static", ".css")
	requireFiles(t, css, "static/css/foundation.css", "static/css/app.css", "static/vendor/bootstrap.min.css", "static/vendor/leaflet.css")
	for path, body := range css {
		check("css", path, assetURLs(body, cssAsset...))
	}

	js := shipped(t, staticFS, "static", ".js")
	requireFiles(t, js, "static/js/map.js", "static/vendor/leaflet.js")
	sawTiles := false
	for path, body := range js {
		check("js", path, assetURLs(body, jsLoad))
		if strings.HasPrefix(path, "static/js/") {
			// Our own scripts: every absolute URL in them is something they load.
			urls := assetURLs(body, jsAbsolute)
			check("js", path, urls)
			for _, u := range urls {
				if h, _ := absHost(u); allowedAssetHost(h) {
					sawTiles = true
				}
			}
		}
	}

	// Positive controls: the patterns caught the stylesheet links, the CSS
	// url()s and the tile layer, so a pass means they looked and found none.
	if checked["template"] == 0 || checked["css"] == 0 {
		t.Errorf("the asset patterns caught nothing (%v): the check cannot fire", checked)
	}
	if !sawTiles {
		t.Error("the tile URL was not found in static/js: the script check cannot fire")
	}
}

// A real page, rendered through the app's own mux: the light theme is declared
// on <html>, and Bootstrap is loaded from this site before the fold and the
// app's own stylesheet.
func TestStylePageDeclaresLightThemeAndLoadsVendoredBootstrap(t *testing.T) {
	app := testApp(t)
	for _, path := range []string{"/", "/photos", "/plan", "/driver/login"} {
		w := send(t, app, "GET", path, "", "", nil)
		if w.Code != 200 {
			t.Fatalf("GET %s: status %d", path, w.Code)
		}
		page := w.Body.String()
		if !regexp.MustCompile(`<html\b[^>]*\bdata-bs-theme="light"`).MatchString(page) {
			t.Errorf("%s: <html> does not carry data-bs-theme=\"light\"", path)
		}
		boot := strings.Index(page, `href="/static/vendor/bootstrap.min.css`)
		fold := strings.Index(page, `href="/static/css/foundation.css`)
		own := strings.Index(page, `href="/static/css/app.css`)
		if boot < 0 || fold < 0 || own < 0 {
			t.Fatalf("%s: a stylesheet is missing (bootstrap %d, foundation %d, app %d)", path, boot, fold, own)
		}
		if !(boot < fold && fold < own) {
			t.Errorf("%s: stylesheets out of order (bootstrap %d, foundation %d, app %d)", path, boot, fold, own)
		}
		if script := strings.Index(page, "<script"); script >= 0 && script < own {
			t.Errorf("%s: a script comes before the stylesheets", path)
		}
	}
}

// Bootstrap 5.3.3 is vendored byte for byte: the published integrity hash and
// the size, read from what the binary embeds.
func TestStyleVendoredBootstrapIsTheRelease(t *testing.T) {
	const (
		wantSHA384 = "QWTKZyjpPEjISv5WaRU9OFeRpok6YctnYmDr5pNlyT2bRjXh0JMhjY6hW+ALEwIH"
		wantSize   = 232803
	)
	b, err := staticFS.ReadFile("static/vendor/bootstrap.min.css")
	if err != nil {
		t.Fatalf("bootstrap is not embedded: %v", err)
	}
	if len(b) != wantSize {
		t.Errorf("bootstrap.min.css is %d bytes, want %d", len(b), wantSize)
	}
	sum := sha512.Sum384(b)
	if got := base64.StdEncoding.EncodeToString(sum[:]); got != wantSHA384 {
		t.Errorf("bootstrap.min.css sha384 %s, want %s", got, wantSHA384)
	}
}

// The foundation copy says which fold of the system it is, first thing.
func TestStyleFoundationOpensWithTheFoldStamp(t *testing.T) {
	b, err := staticFS.ReadFile("static/css/foundation.css")
	if err != nil {
		t.Fatalf("foundation.css is not embedded: %v", err)
	}
	stamp := regexp.MustCompile(`\A/\* Folded from the platform Design System, tokens version (\d+) \(last change (\d{4}-\d{2}-\d{2})\), on (\d{4}-\d{2}-\d{2})\. Changes only by a deliberate re-fold; never refreshed by an automatic step\.`)
	m := stamp.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("foundation.css does not open with the fold stamp (tokens version, last change, fold date)")
	}
	if m[3] < m[2] {
		t.Errorf("the fold date %s is before the tokens' last change %s", m[3], m[2])
	}
}
