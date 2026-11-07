package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postTelemetry(app *App, c *http.Cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/driver/telemetry", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8") // what sendBeacon sends
	req.Header.Set("Origin", "http://"+req.Host)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.requireAuth(app.telemetry).ServeHTTP(rec, req)
	return rec
}

func TestTelemetryAllowlistedEventLands(t *testing.T) {
	f := newFakeRedis(t, true)
	app := testApp(t)
	app.melt = newMelt(f.addr(), "kate")
	c := signedInCookie(t, app)
	rec := postTelemetry(app, c, `{"event":"nav_tap","fields":{"app":"waze","mode":"trip","hidden":false}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	cmds := f.wait(t, 2)
	var rec2 map[string]any
	if err := json.Unmarshal([]byte(cmds[0][2]), &rec2); err != nil {
		t.Fatal(err)
	}
	if rec2["event"] != "nav_tap" || rec2["origin"] != "phone" || rec2["app"] != "waze" || rec2["hidden"] != false || rec2["user"] == "" {
		t.Fatalf("record %v", rec2)
	}
	// js_error: msg truncated, src stripped to a path.
	postTelemetry(app, c, `{"event":"js_error","fields":{"msg":"`+strings.Repeat("x", 300)+`","src":"/static/js/hud.js?v=3","line":12}}`)
	var je map[string]any
	_ = json.Unmarshal([]byte(f.wait(t, 4)[2][2]), &je)
	if len(je["msg"].(string)) != 200 || je["src"] != "/static/js/hud.js" {
		t.Fatalf("js_error %v", je)
	}
}

func TestTelemetryRefusals(t *testing.T) {
	app := testApp(t)
	c := signedInCookie(t, app)
	for name, body := range map[string]string{
		"lat":      `{"event":"geo_error","fields":{"code":1,"lat":30.1}}`,
		"nested":   `{"event":"page_view","fields":{"path":"/driver","x":{"Longitude":1}}}`,
		"unknown":  `{"event":"steal","fields":{}}`,
		"badfield": `{"event":"geo_error","fields":{"code":1,"extra":2}}`,
		"badenum":  `{"event":"nav_tap","fields":{"app":"bing"}}`,
		"notjson":  `nope`,
	} {
		if rec := postTelemetry(app, c, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestTelemetryRateLimit(t *testing.T) {
	app := testApp(t)
	c := signedInCookie(t, app)
	for i := 1; i <= 20; i++ {
		if rec := postTelemetry(app, c, `{"event":"page_view","fields":{"path":"/driver"}}`); rec.Code != http.StatusNoContent {
			t.Fatalf("event %d: status %d", i, rec.Code)
		}
	}
	if rec := postTelemetry(app, c, `{"event":"page_view","fields":{"path":"/driver"}}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("21st event: status %d, want 429", rec.Code)
	}
}
