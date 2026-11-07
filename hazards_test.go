package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The hazard sources are stubbed at the HTTP boundary: nothing here touches
// the network. Each source is tested in its three honest states: it answered
// with items, it answered with none, and it could not answer.

var testPts = []ll{{30.2, -97.7}, {31.0, -100.0}, {31.9, -102.1}}

func hazardSvcFor(nws, tt *httptest.Server, key string) *hazardSvc {
	s := newHazardSvc(key)
	s.timeout = 500 * time.Millisecond
	if nws != nil {
		s.nwsBase = nws.URL
	}
	if tt != nil {
		s.ttBase = tt.URL
	}
	return s
}

func chipTexts(cs []hazardChip) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.State+"|"+c.Text)
	}
	return strings.Join(out, "\n")
}

func TestWeatherAnsweredWithItems(t *testing.T) {
	var ua atomic.Value
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		if !strings.Contains(r.URL.RawQuery, "point=") {
			t.Errorf("no point in %q", r.URL.RawQuery)
		}
		w.Write([]byte(`{"features":[{"properties":{"id":"a1","event":"Severe Thunderstorm Warning","severity":"Severe","areaDesc":"Pecos County"}}]}`))
	}))
	defer nws.Close()
	got := hazardSvcFor(nws, nil, "").Weather(context.Background(), testPts)
	if len(got) != 1 || got[0].State != hazardItems || !strings.Contains(got[0].Text, "Severe Thunderstorm Warning") || !strings.HasPrefix(got[0].Text, "Weather:") {
		t.Fatalf("want one deduped weather item, got:\n%s", chipTexts(got))
	}
	u, _ := ua.Load().(string)
	if u == "" || strings.Contains(u, "@") {
		t.Fatalf("User-Agent must be descriptive and carry no personal email: %q", u)
	}
}

func TestWeatherAnsweredEmptyIsClear(t *testing.T) {
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"features":[]}`)) }))
	defer nws.Close()
	got := hazardSvcFor(nws, nil, "").Weather(context.Background(), testPts)
	if len(got) != 1 || got[0].State != hazardClear {
		t.Fatalf("want one clear chip, got:\n%s", chipTexts(got))
	}
}

func TestWeatherUnavailableIsNeverClear(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) }))
	defer bad.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer slow.Close()
	for name, srv := range map[string]*httptest.Server{"error": bad, "timeout": slow} {
		got := hazardSvcFor(srv, nil, "").Weather(context.Background(), testPts)
		if len(got) != 1 || got[0].State != hazardUnknown || got[0].Text != "Weather: unknown ("+name+")" {
			t.Errorf("%s: want 'Weather: unknown (%s)', got:\n%s", name, name, chipTexts(got))
		}
	}
}

func TestWeatherPartialFailureWithNoItemsIsUnknown(t *testing.T) {
	var n int32
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			http.Error(w, "no", 500)
			return
		}
		w.Write([]byte(`{"features":[]}`))
	}))
	defer nws.Close()
	got := hazardSvcFor(nws, nil, "").Weather(context.Background(), testPts)
	if len(got) != 1 || got[0].State != hazardUnknown {
		t.Fatalf("one failed point must not read as all clear, got:\n%s", chipTexts(got))
	}
}

const ttItems = `{"incidents":[
 {"properties":{"id":"i1","iconCategory":1,"magnitudeOfDelay":3,"events":[{"description":"Stationary traffic"}],"roadNumbers":["I-10"],"from":"Ozona","to":"Sonora","delay":900}},
 {"properties":{"id":"i2","iconCategory":8,"magnitudeOfDelay":4,"events":[{"description":"Road closed"}],"roadNumbers":["US-90"]}},
 {"properties":{"id":"i3","iconCategory":6,"magnitudeOfDelay":1,"events":[{"description":"Slow traffic"}]}}]}`

func TestTrafficAnsweredWithItems(t *testing.T) {
	var mu sync.Mutex
	var gotKey, gotBBox string
	tt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotKey, gotBBox = r.URL.Query().Get("key"), r.URL.Query().Get("bbox")
		mu.Unlock()
		w.Write([]byte(ttItems))
	}))
	defer tt.Close()
	got := hazardSvcFor(nil, tt, "k-test").Traffic(context.Background(), testPts)
	mu.Lock()
	defer mu.Unlock()
	if gotKey != "k-test" || strings.Count(gotBBox, ",") != 3 {
		t.Fatalf("key %q bbox %q", gotKey, gotBBox)
	}
	// i1 and i2 are major or closed; the minor one is not a chip. The same
	// incidents come back for every box and are shown once.
	if len(got) != 2 || got[0].State != hazardItems || !strings.Contains(got[0].Text, "Road closed") || !strings.Contains(got[1].Text, "Stationary traffic") {
		t.Fatalf("want closure first then the major delay, deduped:\n%s", chipTexts(got))
	}
}

func TestTrafficAnsweredEmptyIsClear(t *testing.T) {
	tt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"incidents":[]}`)) }))
	defer tt.Close()
	got := hazardSvcFor(nil, tt, "k").Traffic(context.Background(), testPts)
	if len(got) != 1 || got[0].State != hazardClear || got[0].Text != "Traffic: no incidents" {
		t.Fatalf("got:\n%s", chipTexts(got))
	}
}

func TestTrafficUnavailableStates(t *testing.T) {
	var hits int32
	tt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }))
	defer tt.Close()
	// No key: unknown, and the service is never asked.
	got := hazardSvcFor(nil, tt, "").Traffic(context.Background(), testPts)
	if len(got) != 1 || got[0].State != hazardUnknown || got[0].Text != "Traffic: unknown (no key)" || atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("no key: %s hits=%d", chipTexts(got), hits)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "key=secret-k bad", 403) }))
	defer bad.Close()
	got = hazardSvcFor(nil, bad, "secret-k").Traffic(context.Background(), testPts)
	if len(got) != 1 || got[0].Text != "Traffic: unknown (error)" || strings.Contains(chipTexts(got), "secret-k") {
		t.Fatalf("error: %s", chipTexts(got))
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer slow.Close()
	got = hazardSvcFor(nil, slow, "k").Traffic(context.Background(), testPts)
	if len(got) != 1 || got[0].Text != "Traffic: unknown (timeout)" {
		t.Fatalf("timeout: %s", chipTexts(got))
	}
}

func TestHazardsAreCachedAndUnknownIsCachedShort(t *testing.T) {
	var hits int32
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"features":[]}`))
	}))
	defer nws.Close()
	s := hazardSvcFor(nws, nil, "")
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.ForLeg(context.Background(), "leg-1", testPts)
	after := atomic.LoadInt32(&hits)
	s.ForLeg(context.Background(), "leg-1", testPts)
	if atomic.LoadInt32(&hits) != after {
		t.Fatal("a second call inside the cache window went to the network")
	}
	now = now.Add(11 * time.Minute)
	s.ForLeg(context.Background(), "leg-1", testPts)
	if atomic.LoadInt32(&hits) == after {
		t.Fatal("the cache never expired")
	}
}

func TestHazardsNoRouteIsUnknown(t *testing.T) {
	got := newHazardSvc("k").Weather(context.Background(), nil)
	if len(got) != 1 || got[0].State != hazardUnknown {
		t.Fatalf("got:\n%s", chipTexts(got))
	}
}
