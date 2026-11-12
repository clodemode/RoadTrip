package main

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/post_form_by_type.feature. They ride the post world and
// read the rendered page the way a browser would see it before any script runs.

var (
	detailsRe  = regexp.MustCompile(`(?s)<details[^>]*id="more-options"[^>]*>.*?</details>`)
	inputTagRe = regexp.MustCompile(`<input[^>]*>`)
)

func initPostFormByTypeScenarios(sc *godog.ScenarioContext, p *postWorld) {
	sc.Step(`^kris is in "(trip|aimless|destined)" mode$`, func(mode string) error {
		p.sim.app.freeSvc().set("kris", func(s *freeState) { s.enter(mode) })
		return nil
	})
	sc.Step(`^the page never picks No pin by itself$`, func() error {
		js, err := os.ReadFile("static/js/post.js")
		if err != nil {
			return err
		}
		if strings.Contains(string(js), "rNone.checked = true") {
			return fmt.Errorf("post.js selects No pin on its own")
		}
		return nil
	})
	postJS := func() (string, error) {
		js, err := os.ReadFile("static/js/post.js")
		return string(js), err
	}
	sc.Step(`^a photo with no GPS is explained as normal for a camera shot$`, func() error {
		js, err := postJS()
		if err != nil {
			return err
		}
		if strings.Contains(js, "none in this file") {
			return fmt.Errorf("post.js still reports a camera shot as none in this file")
		}
		if !strings.Contains(js, "normal for a camera shot") {
			return fmt.Errorf("post.js does not say a camera shot normally has no GPS")
		}
		return nil
	})
	sc.Step(`^that line says whether the phone's location will pin the post$`, func() error {
		js, err := postJS()
		if err != nil {
			return err
		}
		for _, want := range []string{"location will pin it", "has not shared its location"} {
			if !strings.Contains(js, want) {
				return fmt.Errorf("post.js does not say %q", want)
			}
		}
		if n := strings.Count(js, "sayNoExif();"); n < 3 {
			return fmt.Errorf("the line is refreshed in %d places, want the photo read and both location answers", n)
		}
		return nil
	})
	sc.Step(`^the form has a "kind" button for every post kind$`, func() error {
		for _, k := range postKinds {
			if !regexp.MustCompile(`<button[^>]*type="submit"[^>]*name="kind"[^>]*value="` + k.Value + `"[^>]*>`).MatchString(p.body) {
				return fmt.Errorf("no kind button for %q", k.Value)
			}
		}
		return nil
	})
	sc.Step(`^no kind is pre-selected$`, func() error {
		for _, t := range inputTagRe.FindAllString(p.body, -1) {
			if strings.Contains(t, `name="kind"`) {
				return fmt.Errorf("the form carries a kind field of its own: %s", t)
			}
		}
		return nil
	})
	sc.Step(`^the form is in start mode for A$`, func() error {
		if !strings.Contains(p.body, "<h1>Start the drive</h1>") {
			return fmt.Errorf("heading is not Start the drive")
		}
		id := strconv.FormatInt(p.cps[0].ID, 10)
		ok := false
		for _, t := range radioRe.FindAllString(p.body, -1) {
			if strings.Contains(t, `value="`+id+`"`) && strings.Contains(t, "checked") {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("the start radio is not selected")
		}
		if !regexp.MustCompile(`id="post-types"[^>]*\bhidden\b`).MatchString(p.body) {
			return fmt.Errorf("the type chooser is not hidden")
		}
		if !strings.Contains(p.body, "Start the drive from "+p.cps[0].Name) {
			return fmt.Errorf("no submit reading Start the drive from %s", p.cps[0].Name)
		}
		return nil
	})
	sc.Step(`^the form offers "(Post something else instead|Post a Photo)"$`, func(s string) error {
		if !strings.Contains(p.body, s) {
			return fmt.Errorf("the form does not say %q", s)
		}
		return nil
	})
	sc.Step(`^the form does not offer "Post something else instead"$`, func() error {
		if strings.Contains(p.body, "Post something else instead") {
			return fmt.Errorf("the form still offers Post something else instead")
		}
		return nil
	})
	sc.Step(`^the form offers no start$`, func() error {
		if strings.Contains(p.body, "Start the drive") || len(radioRe.FindAllString(p.body, -1)) != 0 {
			return fmt.Errorf("the form still offers a start")
		}
		return nil
	})
	sc.Step(`^the driver screen's photo tool reads "([^"]*)"$`, func(label string) error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", "/driver", nil)
		re := regexp.MustCompile(`<a class="hud-tool" href="/driver/post">(?:<span[^>]*>[^<]*</span>)?\s*([^<]*)</a>`)
		m := re.FindStringSubmatch(w.Body.String())
		if m == nil {
			return fmt.Errorf("no photo tool on the driver screen")
		}
		if strings.TrimSpace(m[1]) != label {
			return fmt.Errorf("the photo tool reads %q, want %q", strings.TrimSpace(m[1]), label)
		}
		return nil
	})
	sc.Step(`^More options holds Leg, Zone, Checkpoint, When and Pin$`, func() error {
		d := detailsRe.FindString(p.body)
		if d == "" {
			return fmt.Errorf("no More options details element")
		}
		for _, n := range []string{`name="leg_id"`, `name="tz"`, `name="checkpoint_id"`, `name="when"`, `name="pin_source"`} {
			if !strings.Contains(d, n) {
				return fmt.Errorf("More options does not hold %s", n)
			}
		}
		return nil
	})
	sc.Step(`^"Fuzz This" is inside More options$`, func() error {
		if d := detailsRe.FindString(p.body); !strings.Contains(d, "Fuzz This") || !strings.Contains(d, `id="f-fuzz"`) {
			return fmt.Errorf("Fuzz This is not inside More options")
		}
		return nil
	})
	sc.Step(`^kris asks for the full form$`, func() error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", "/driver/post?full=1", nil)
		p.body = w.Body.String()
		return nil
	})
	sc.Step(`^kris posts a note with no photo$`, func() error {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("leg_id", strconv.FormatInt(p.cps[0].LegID, 10))
		_ = mw.WriteField("kind", "note")
		_ = mw.WriteField("title", "Gas stop")
		_ = mw.WriteField("body", "Filled up.")
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/driver/post", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(p.sim.cookies["kris"])
		req = req.WithContext(context.Background())
		w := httptest.NewRecorder()
		p.sim.app.routes().ServeHTTP(w, req)
		p.last = w
		if w.Code >= http.StatusBadRequest {
			return fmt.Errorf("a note was refused: %d %s", w.Code, w.Body.String())
		}
		return nil
	})
}
