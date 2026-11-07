package main

// MELT: the fleet's observability bus is Redis, and its readers (melt_tail,
// melt_status) read lists at clode:state:melt:{source}:{stream}, newest at
// index 0, capped at 1000. This is a fire-and-forget publisher for it. It
// speaks the little RESP it needs over net.Dial (no dependency), never makes
// a request handler wait on Redis, and refuses any record that could carry a
// place or a private word: no coordinate, typed destination or note ever
// reaches the bus.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	meltSource  = "roadtrip"
	meltCap     = 1000
	meltQueue   = 256
	meltIOLimit = 300 * time.Millisecond
)

// meltForbidden are field keys that may never reach the bus.
var meltForbidden = map[string]bool{
	"lat": true, "lng": true, "latitude": true, "longitude": true, "coords": true,
	"address": true, "note": true, "private": true, "dest": true, "label": true, "q": true,
}

type meltItem struct {
	key  string
	body []byte
}

type meltSink struct {
	addr, venue string
	ch          chan meltItem
	drops       atomic.Int64
	warned      sync.Map
}

// newMelt returns nil (off) when addr is empty. A nil *meltSink is safe to call.
func newMelt(addr, venue string) *meltSink {
	if strings.TrimSpace(addr) == "" {
		log.Print("melt: off (MELT_REDIS_ADDR unset)")
		return nil
	}
	if venue == "" {
		venue = "kate"
	}
	m := &meltSink{addr: addr, venue: venue, ch: make(chan meltItem, meltQueue)}
	go m.drain()
	go m.reportDrops()
	log.Printf("melt: on (%s, venue %s)", addr, venue)
	return m
}

// Event publishes to the events stream; Error to the errors stream.
func (m *meltSink) Event(name string, f map[string]any) { m.publish("events", name, f) }
func (m *meltSink) Error(name string, f map[string]any) { m.publish("errors", name, f) }

func (m *meltSink) publish(stream, name string, f map[string]any) {
	if m == nil {
		return
	}
	for k := range f {
		if meltForbidden[strings.ToLower(k)] {
			if _, seen := m.warned.LoadOrStore(k, true); !seen {
				log.Printf("melt: refused an event carrying the field %q (no coordinates, destinations or private text on the bus)", k)
			}
			return
		}
	}
	rec := map[string]any{}
	for k, v := range f {
		if v == nil || v == "" {
			continue
		}
		rec[k] = v
	}
	rec["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	rec["source"] = meltSource
	rec["event"] = name
	if _, ok := rec["origin"]; !ok {
		rec["origin"] = "server"
	}
	rec["venue"] = m.venue
	body, err := json.Marshal(rec)
	if err != nil {
		return
	}
	select {
	case m.ch <- meltItem{key: "clode:state:melt:" + meltSource + ":" + stream, body: body}:
	default:
		m.drops.Add(1)
	}
}

func (m *meltSink) reportDrops() {
	var last int64
	for range time.Tick(time.Minute) {
		if n := m.drops.Load(); n != last {
			log.Printf("melt: %d record(s) dropped, the queue was full", n-last)
			last = n
		}
	}
}

func resp(args ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return b.String()
}

func (m *meltSink) drain() {
	var conn net.Conn
	var rd *bufio.Reader
	for it := range m.ch {
		if conn == nil {
			c, err := net.DialTimeout("tcp", m.addr, meltIOLimit)
			if err != nil {
				m.drops.Add(1)
				continue
			}
			conn, rd = c, bufio.NewReader(c)
		}
		_ = conn.SetDeadline(time.Now().Add(meltIOLimit))
		payload := resp("LPUSH", it.key, string(it.body)) + resp("LTRIM", it.key, "0", fmt.Sprint(meltCap-1))
		_, err := conn.Write([]byte(payload))
		for i := 0; err == nil && i < 2; i++ {
			_, err = rd.ReadString('\n')
		}
		if err != nil {
			m.drops.Add(1)
			conn.Close()
			conn, rd = nil, nil
		}
	}
}
