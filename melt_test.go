package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRedis parses the RESP it receives and records each command.
type fakeRedis struct {
	ln   net.Listener
	mu   sync.Mutex
	cmds [][]string
}

func newFakeRedis(t *testing.T, reply bool) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRedis{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c, reply)
		}
	}()
	return f
}

func (f *fakeRedis) addr() string { return f.ln.Addr().String() }

func (f *fakeRedis) serve(c net.Conn, reply bool) {
	defer c.Close()
	if !reply { // a stalled server: accepts, reads nothing
		_, _ = io.Copy(io.Discard, io.LimitReader(neverReader{}, 0))
		time.Sleep(5 * time.Second)
		return
	}
	rd := bufio.NewReader(c)
	for {
		line, err := rd.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "*") {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
		var args []string
		for i := 0; i < n; i++ {
			l, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			size, _ := strconv.Atoi(strings.TrimSpace(l[1:]))
			buf := make([]byte, size+2)
			if _, err := io.ReadFull(rd, buf); err != nil {
				return
			}
			args = append(args, string(buf[:size]))
		}
		f.mu.Lock()
		f.cmds = append(f.cmds, args)
		f.mu.Unlock()
		_, _ = c.Write([]byte(":1\r\n"))
	}
}

type neverReader struct{}

func (neverReader) Read([]byte) (int, error) { return 0, io.EOF }

func (f *fakeRedis) wait(t *testing.T, n int) [][]string {
	t.Helper()
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		if len(f.cmds) >= n {
			out := append([][]string(nil), f.cmds...)
			f.mu.Unlock()
			return out
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("wanted %d commands, got %v", n, f.cmds)
	return nil
}

func (f *fakeRedis) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cmds)
}

func TestMeltPushesLpushThenLtrim(t *testing.T) {
	f := newFakeRedis(t, true)
	m := newMelt(f.addr(), "")
	m.Event("hud_served", map[string]any{"mode": "trip", "has_fix": true, "empty": ""})
	cmds := f.wait(t, 2)
	if cmds[0][0] != "LPUSH" || cmds[0][1] != "clode:state:melt:roadtrip:events" {
		t.Fatalf("first command %v", cmds[0])
	}
	if strings.Join(cmds[1], " ") != "LTRIM clode:state:melt:roadtrip:events 0 999" {
		t.Fatalf("second command %v", cmds[1])
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(cmds[0][2]), &rec); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"source": "roadtrip", "event": "hud_served", "origin": "server", "venue": "kate", "mode": "trip", "has_fix": true} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %v", k, rec[k], want)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, rec["ts"].(string)); err != nil {
		t.Errorf("ts: %v", err)
	}
	if _, has := rec["empty"]; has {
		t.Error("an empty field was kept")
	}
	m.Error("http_5xx", map[string]any{"path": "/x", "status": 500})
	if c := f.wait(t, 4); c[2][1] != "clode:state:melt:roadtrip:errors" {
		t.Errorf("error stream key %v", c[2])
	}
}

func TestMeltGuardDropsLat(t *testing.T) {
	f := newFakeRedis(t, true)
	m := newMelt(f.addr(), "kate")
	m.Event("bad", map[string]any{"lat": 30.1})
	m.Event("good", map[string]any{"mode": "trip"})
	cmds := f.wait(t, 2)
	if !strings.Contains(cmds[0][2], `"good"`) {
		t.Fatalf("the first record to arrive was not the clean one: %v", cmds[0])
	}
	time.Sleep(100 * time.Millisecond)
	if f.count() != 2 {
		t.Fatalf("a refused record reached the bus: %v", f.cmds)
	}
}

// Control: first run against a deliberately wrong expectation (that a record
// carrying lng is delivered), which failed; corrected to the real contract.
func TestMeltGuardDropsLngControl(t *testing.T) {
	f := newFakeRedis(t, true)
	m := newMelt(f.addr(), "kate")
	m.Event("bad", map[string]any{"lng": -97.7})
	time.Sleep(150 * time.Millisecond)
	if f.count() != 0 {
		t.Fatalf("a record carrying lng reached the bus: %d commands", f.count())
	}
}

func TestMeltNilAndStalledNeverBlock(t *testing.T) {
	var off *meltSink
	off.Event("x", nil)
	off.Error("x", nil)
	if newMelt("", "") != nil {
		t.Fatal("empty addr must be off")
	}
	f := newFakeRedis(t, false)
	m := newMelt(f.addr(), "kate")
	for i := 0; i < 600; i++ {
		start := time.Now()
		m.Event("tick", map[string]any{"n": i})
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Fatalf("Event blocked for %v", d)
		}
	}
}
