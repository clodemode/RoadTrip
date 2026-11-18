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
// as the live one behaved: it calls the tool the last words it was sent ask
// for, and otherwise answers in words. The API refuses a forced tool on the
// Chester models (2026-11-18), so the words are the only push there is.

func registerDriveAlbumSteps(sc interface{ Step(expr, stepFunc any) }, w *dtWorld) {
	sc.Step(`^the model obeys the tool it is told to call and otherwise answers in words$`, w.obeys)
	sc.Step(`^every request to the model asked for adaptive thinking$`, w.allAdaptive)
	sc.Step(`^the last request to the model asked in words for show_music$`, func() error {
		reqs := w.stub.requests()
		if len(reqs) == 0 || lastWords(reqs[len(reqs)-1].Body) != chesterShowNow {
			return fmt.Errorf("the last request did not end with the words asking for show_music")
		}
		return nil
	})
	sc.Step(`^no request to the model forced a tool$`, func() error { return w.noForce("") })
	sc.Step(`^no request to the model forced the tool "([^"]*)"$`, w.noForce)
	sc.Step(`^the trip-keeper types "([^"]*)" to Chester$`, w.typeAsk)
}

// lastWords is the last text block of the last user message of a request
// body: the words the traveller's side sent last, after any tool results. A
// system message that withdraws the searches may follow it and is skipped.
func lastWords(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	i := len(req.Messages) - 1
	for i >= 0 && req.Messages[i].Role != "user" {
		i--
	}
	if i < 0 {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(req.Messages[i].Content, &blocks) != nil {
		return ""
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Type == "text" {
			return blocks[i].Text
		}
	}
	return ""
}

// obeying is one answer of the obedient model.
func (w *dtWorld) obeying() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		// The stub has already read the body off the wire; its copy is the last.
		reqs := w.stub.requests()
		said := lastWords(reqs[len(reqs)-1].Body)
		n := strconv.Itoa(len(reqs))
		var evs []string
		switch said {
		case chesterSearchNow:
			evs = events(evStart(stubModel, 100, 0, 0),
				evClientTool(0, "toolu_os"+n, "search_music", js(map[string]any{"query": "the beatles"})), evEnd("tool_use", 20, 0, nil))
		case chesterShowNow:
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

func (w *dtWorld) allAdaptive() error {
	for i := range w.stub.requests() {
		_, _, thinking, err := w.toolChoice(i)
		if err != nil {
			return err
		}
		if thinking != "adaptive" {
			return fmt.Errorf("request %d asked for %q thinking; the Chester models take only adaptive", i, thinking)
		}
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
