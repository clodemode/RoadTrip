package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/post_one_tap.feature. A "tap" reads the rendered form the
// way the page's script does: it finds the type's button, the title and note
// fields by their ids, and posts only what that tap would send. Nothing is
// sent that the page does not carry, so a field that loses its name, or a
// button that is missing, fails here.

var (
	kindButtonRe = regexp.MustCompile(`(?s)<button[^>]*name="kind"[^>]*value="([^"]*)"[^>]*>\s*([^<]*?)\s*</button>`)
	nameAttrRe   = regexp.MustCompile(`name="([^"]*)"`)
)

type oneTapWorld struct {
	p     *postWorld
	title string
	note  string
	fuzz  bool
}

func initPostOneTapScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &oneTapWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = oneTapWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^the form has a button for every post kind$`, func() error {
		for _, k := range postKinds {
			if got := w.buttonFor(k.Label); got != k.Value {
				return fmt.Errorf("no button reading %q that posts kind %q (found %q)", k.Label, k.Value, got)
			}
		}
		return nil
	})
	sc.Step(`^an optional title and an optional note sit above the buttons$`, func() error {
		rest := detailsRe.ReplaceAllString(p.body, "")
		ti, bi, ki := strings.Index(rest, `id="f-title"`), strings.Index(rest, `id="f-body"`), strings.Index(rest, `name="kind"`)
		if ti < 0 || bi < 0 || ki < 0 {
			return fmt.Errorf("title at %d, note at %d, first kind button at %d, all must be in plain view", ti, bi, ki)
		}
		if ti > ki || bi > ki {
			return fmt.Errorf("the title and note come after the kind buttons")
		}
		for _, id := range []string{"f-title", "f-body"} {
			if strings.Contains(w.tagWithID(id), "required") {
				return fmt.Errorf("%s is required, it must be optional", id)
			}
		}
		return nil
	})
	sc.Step(`^the page says to type a title or note first, then tap what is being posted$`, func() error {
		low := strings.ToLower(p.body)
		for _, want := range []string{"type a title or note first", "then tap what you are posting", `id="text-need"`} {
			if !strings.Contains(low, want) {
				return fmt.Errorf("the page does not say %q", want)
			}
		}
		return nil
	})
	sc.Step(`^kris taps "([^"]*)" and picks a photo with nothing typed$`, func(label string) error { return w.tap(label, true) })
	sc.Step(`^kris types the title "([^"]*)" and taps "([^"]*)" and picks a photo$`, func(t, label string) error {
		w.title = t
		return w.tap(label, true)
	})
	sc.Step(`^kris types the note "([^"]*)" and taps "([^"]*)" and picks a photo$`, func(n, label string) error {
		w.note = n
		return w.tap(label, true)
	})
	sc.Step(`^kris ticks Fuzz This on the post form$`, func() error {
		r := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", "/driver/post", nil)
		p.body = r.Body.String()
		if strings.Contains(detailsRe.FindString(p.body), `id="f-fuzz"`) || !strings.Contains(detailsRe.ReplaceAllString(p.body, ""), `id="f-fuzz"`) {
			return fmt.Errorf("Fuzz This is not on the form outside More options, so it cannot be ticked before the tap")
		}
		w.fuzz = true
		return nil
	})
	sc.Step(`^kris taps "([^"]*)" with nothing typed$`, func(label string) error { return w.tap(label, false) })
	sc.Step(`^kris types the note "([^"]*)" and taps "([^"]*)"$`, func(n, label string) error {
		w.note = n
		return w.tap(label, false)
	})
	sc.Step(`^kris taps "([^"]*)" and picks a photo taken at B's exact point$`, func(label string) error {
		p.photo = jpegGPSAt("2026:11:07 12:00:00", bRawLat, bRawLng)
		return w.tap(label, true)
	})
	sc.Step(`^kris is told to say something first$`, func() error {
		l := p.last
		loc := l.Header().Get("Location")
		if l.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/driver/post?error=") {
			return fmt.Errorf("answer %d to %q, want a redirect back to the form with a sentence", l.Code, loc)
		}
		q, err := url.Parse(loc)
		if err != nil {
			return err
		}
		if msg := q.Query().Get("error"); !strings.Contains(msg, "Say something") {
			return fmt.Errorf("the sentence is %q", msg)
		}
		return nil
	})
	sc.Step(`^no post was made$`, func() error {
		posts, err := p.sim.app.store.Posts("post-photo-trip", 0)
		if err != nil {
			return err
		}
		if len(posts) != 0 {
			return fmt.Errorf("%d posts were made", len(posts))
		}
		return nil
	})
	sc.Step(`^the post is a "([^"]*)"$`, func(kind string) error {
		post, err := w.thePost()
		if err != nil {
			return err
		}
		if post.Kind != kind {
			return fmt.Errorf("the post is a %q, want %q", post.Kind, kind)
		}
		return nil
	})
	sc.Step(`^the post's title is "([^"]*)"$`, func(want string) error {
		post, err := w.thePost()
		if err != nil {
			return err
		}
		if post.Title != want {
			return fmt.Errorf("the title is %q, want %q", post.Title, want)
		}
		return nil
	})
	sc.Step(`^the post's note is "([^"]*)"$`, func(want string) error {
		post, err := w.thePost()
		if err != nil {
			return err
		}
		if post.Body != want {
			return fmt.Errorf("the note is %q, want %q", post.Body, want)
		}
		return nil
	})
	sc.Step(`^the post is published as a fuzzed cell, not the exact point$`, func() error {
		post, err := w.thePost()
		if err != nil {
			return err
		}
		if !post.PinFuzzed || post.PinLat == nil || post.PinLng == nil {
			return fmt.Errorf("the post is not published fuzzed (pin_fuzzed %v)", post.PinFuzzed)
		}
		if math.Abs(*post.PinLat-bRawLat) < 1e-4 && math.Abs(*post.PinLng-bRawLng) < 1e-4 {
			return fmt.Errorf("the published point is the exact one")
		}
		return nil
	})
	sc.Step(`^the form has no post type radio row$`, func() error {
		if regexp.MustCompile(`<input[^>]*type="radio"[^>]*name="kind"`).MatchString(p.body) ||
			regexp.MustCompile(`<input[^>]*name="kind"[^>]*type="radio"`).MatchString(p.body) {
			return fmt.Errorf("the form still has a kind radio")
		}
		return nil
	})
	sc.Step(`^the Post button is inside More options$`, func() error {
		d := detailsRe.FindString(p.body)
		if !regexp.MustCompile(`(?s)<button[^>]*id="btn-post"[^>]*>\s*Post\s*</button>`).MatchString(d) {
			return fmt.Errorf("no Post button inside More options")
		}
		return nil
	})
	sc.Step(`^everything else on the form is inside More options$`, func() error {
		d := detailsRe.FindString(p.body)
		for _, n := range []string{`name="tz"`, `name="leg_id"`, `name="checkpoint_id"`, `name="when"`, `name="pin_source"`} {
			if !strings.Contains(d, n) {
				return fmt.Errorf("More options does not hold %s", n)
			}
		}
		return nil
	})
}

// buttonFor is the kind a button with that label posts, or "".
func (w *oneTapWorld) buttonFor(label string) string {
	for _, m := range kindButtonRe.FindAllStringSubmatch(w.p.body, -1) {
		if m[2] == label {
			return m[1]
		}
	}
	return ""
}

func (w *oneTapWorld) tagWithID(id string) string {
	return regexp.MustCompile(`<(?:input|textarea)[^>]*id="` + id + `"[^>]*>`).FindString(w.p.body)
}

// fieldName is the name the page gives the field with that id.
func (w *oneTapWorld) fieldName(id string) (string, error) {
	m := nameAttrRe.FindStringSubmatch(w.tagWithID(id))
	if m == nil {
		return "", fmt.Errorf("the page has no field %s with a name", id)
	}
	return m[1], nil
}

// tap opens the form, then sends what one tap on the button sends: the type,
// whatever was typed first and, for a type that carries a photo, the photo.
func (w *oneTapWorld) tap(label string, withPhoto bool) error {
	p := w.p
	r := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", "/driver/post", nil)
	p.body = r.Body.String()
	kind := w.buttonFor(label)
	if kind == "" {
		return fmt.Errorf("the form has no %q button", label)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("kind", kind)
	if w.title != "" {
		n, err := w.fieldName("f-title")
		if err != nil {
			return err
		}
		_ = mw.WriteField(n, w.title)
	}
	if w.note != "" {
		n, err := w.fieldName("f-body")
		if err != nil {
			return err
		}
		_ = mw.WriteField(n, w.note)
	}
	if w.fuzz {
		n, err := w.fieldName("f-fuzz")
		if err != nil {
			return err
		}
		_ = mw.WriteField(n, "1")
		if fz, err := w.fieldName("f-fuzzset"); err == nil {
			_ = mw.WriteField(fz, "1")
		}
	}
	if withPhoto {
		if p.photo == nil {
			p.photo = jpegTaken("")
		}
		fw, _ := mw.CreateFormFile("photo", "p.jpg")
		_, _ = fw.Write(p.photo)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/driver/post", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(p.sim.cookies["kris"])
	rec := httptest.NewRecorder()
	p.sim.app.routes().ServeHTTP(rec, req)
	p.last = rec
	return nil
}

func (w *oneTapWorld) thePost() (*Post, error) {
	posts, err := w.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return nil, err
	}
	if len(posts) != 1 {
		return nil, fmt.Errorf("want one post, have %d (last answer %d %s)", len(posts), w.p.last.Code, w.p.last.Body.String())
	}
	return &posts[0], nil
}
