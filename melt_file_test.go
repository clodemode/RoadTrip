package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitRecords polls the file sink until n records match event.
func waitRecords(t *testing.T, m *meltSink, event string, n int) []map[string]any {
	t.Helper()
	for i := 0; i < 200; i++ {
		if recs := m.readFile("", event, 1000); len(recs) >= n {
			return recs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d %q records were written", n, event)
	return nil
}

func TestMeltFileRotatesAtTwoMegabytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "melt", "roadtrip.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"event":"old","stream":"events"}` + "\n"
	if err := os.WriteFile(path, []byte(strings.Repeat(old, meltFileMax/len(old)+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newMeltSinks("", "test", path)
	m.Event("fresh", map[string]any{"n": 1})
	waitRecords(t, m, "fresh", 1)
	b, err := os.ReadFile(path)
	if err != nil || strings.Count(string(b), "\n") != 1 {
		t.Fatalf("the live file holds %d lines after rotation (%v)", strings.Count(string(b), "\n"), err)
	}
	if fi, err := os.Stat(path + ".1"); err != nil || fi.Size() < meltFileMax {
		t.Fatalf("the old generation was not kept: %v", err)
	}
	// Newest first, across both generations.
	recs := m.readFile("", "", 3)
	if len(recs) != 3 || recs[0]["event"] != "fresh" || recs[1]["event"] != "old" {
		t.Fatalf("read %v", recs)
	}
}

func TestMeltFileAndRedisBothOff(t *testing.T) {
	if newMeltSinks("", "", "") != nil {
		t.Fatal("both sinks unset must be off")
	}
}

func TestRoadtracksAlbumFailureIsAnErrorWithReasonAndMs(t *testing.T) {
	mo := newMO(t)
	mo.mu.Lock()
	mo.status = 503
	mo.mu.Unlock()
	app, c := tracksApp(t, mo)
	app.melt = newMeltSinks("", "test", filepath.Join(t.TempDir(), "roadtrip.jsonl"))
	if rec := doAs(t, app, c, "GET", "/driver/roadtracks/album/42", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("album answered %d", rec.Code)
	}
	r := waitRecords(t, app.melt, "roadtracks_album", 1)[0]
	if r["stream"] != "errors" || r["reason"] != "status_503" {
		t.Fatalf("the record is %v", r)
	}
	if _, ok := r["ms"].(float64); !ok {
		t.Fatalf("the record has no ms: %v", r)
	}
	mo.mu.Lock()
	mo.status = 200
	mo.mu.Unlock()
	if rec := doAs(t, app, c, "GET", "/driver/roadtracks/search?q=abbey", nil); rec.Code != http.StatusOK {
		t.Fatalf("search answered %d", rec.Code)
	}
	s := waitRecords(t, app.melt, "roadtracks_search", 1)[0]
	if _, ok := s["ms"].(float64); !ok || s["stream"] != "events" {
		t.Fatalf("the search record is %v", s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), rtKey) || strings.Contains(string(b), mo.srv.URL) {
		t.Fatalf("the record holds the key or the address: %s", b)
	}
}
