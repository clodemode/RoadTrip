package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Steps for features/drive_tracks_always_returns_an_album.feature. They run on
// the dtWorld of drive_tracks_steps_test.go: the Messages API is the apiStub
// and nothing here reaches Anthropic. The "obedient" model is the stub's model
// as the live one behaved: it calls the tool the request forces (tool_choice)
// and, when nothing is forced, answers in words.

func registerDriveAlbumSteps(sc interface{ Step(expr, stepFunc any) }, w *dtWorld) {
	sc.Step(`^the model obeys the tool it is told to call and otherwise answers in words$`, w.obeys)
	sc.Step(`^the first request to the model forced the tool "([^"]*)"$`, func(tool string) error { return w.forced(0, tool) })
	sc.Step(`^the last request to the model forced the tool "([^"]*)"$`, func(tool string) error {
		return w.forced(len(w.stub.requests())-1, tool)
	})
	sc.Step(`^the first request to the model did not ask for adaptive thinking$`, w.noAdaptiveOnForced)
	sc.Step(`^no request to the model forced a tool$`, func() error { return w.noForce("") })
	sc.Step(`^no request to the model forced the tool "([^"]*)"$`, w.noForce)
	sc.Step(`^the trip-keeper types "([^"]*)" to Chester$`, w.typeAsk)
}

// obeying is one answer of the obedient model.
func (w *dtWorld) obeying() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		// The stub has already read the body off the wire; its copy is the last.
		reqs := w.stub.requests()
		b := reqs[len(reqs)-1].Body
		var req struct {
			ToolChoice struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tool_choice"`
		}
		_ = json.Unmarshal(b, &req)
		n := strconv.Itoa(len(reqs))
		var evs []string
		switch {
		case req.ToolChoice.Type == "tool" && req.ToolChoice.Name == chesterSearchMusicTool:
			evs = events(evStart(stubModel, 100, 0, 0),
				evClientTool(0, "toolu_os"+n, "search_music", js(map[string]any{"query": "the beatles"})), evEnd("tool_use", 20, 0, nil))
		case req.ToolChoice.Type == "tool" && req.ToolChoice.Name == chesterShowMusicTool:
			evs = events(evStart(stubModel, 120, 0, 0),
				evClientTool(0, "toolu_om"+n, "show_music",
					js(map[string]any{"albums": []any{map[string]any{"ref": "m1", "why": "Familiar and unhurried for the open road"}}})),
				evEnd("tool_use", 20, 0, nil))
		default:
			evs = events(evStart(stubModel, 150, 0, 0), evText(0, "Something mellow would suit this stretch."), evEnd("end_turn", 10, 0, nil))
		}
		stream(evs...)(rw, r)
	}
}

func (w *dtWorld) obeys() error {
	for i := 0; i < 3; i++ {
		w.script = append(w.script, w.obeying())
	}
	return nil
}

func (w *dtWorld) toolChoice(n int) (typ, name string, thinking string, err error) {
	reqs := w.stub.requests()
	if n < 0 || n >= len(reqs) {
		return "", "", "", fmt.Errorf("the model was asked %d time(s); there is no request %d", len(reqs), n)
	}
	var req struct {
		ToolChoice struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tool_choice"`
		Thinking struct {
			Type string `json:"type"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(reqs[n].Body, &req); err != nil {
		return "", "", "", err
	}
	return req.ToolChoice.Type, req.ToolChoice.Name, req.Thinking.Type, nil
}

func (w *dtWorld) forced(n int, tool string) error {
	typ, name, _, err := w.toolChoice(n)
	if err != nil {
		return err
	}
	if typ != "tool" || name != tool {
		return fmt.Errorf("request %d carried tool_choice %q %q, want the tool %q", n, typ, name, tool)
	}
	return nil
}

func (w *dtWorld) noAdaptiveOnForced() error {
	_, _, thinking, err := w.toolChoice(0)
	if err != nil {
		return err
	}
	if thinking == "adaptive" || thinking == "enabled" {
		return fmt.Errorf("a request that forces a tool asked for %q thinking, which the API refuses", thinking)
	}
	return nil
}

func (w *dtWorld) noForce(tool string) error {
	for i := range w.stub.requests() {
		typ, name, _, err := w.toolChoice(i)
		if err != nil {
			return err
		}
		if typ == "tool" && (tool == "" || name == tool) {
			return fmt.Errorf("request %d forced the tool %q", i, name)
		}
	}
	return nil
}

func (w *dtWorld) typeAsk(q string) error {
	if err := w.build(); err != nil {
		return err
	}
	rec := doAs(w.t, w.app, w.kris, "POST", "/chester/ask", url.Values{"q": {q}})
	w.code, w.raw = rec.Code, rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(w.raw, `"t":"error"`) {
		return fmt.Errorf("the question was not answered: %d %s", rec.Code, w.raw)
	}
	return nil
}
