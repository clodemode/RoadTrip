package main

// The MELT file sink and its signed-in read. Fly has no fleet Redis, so in
// production the trouble records are kept as one JSON line each in MELT_FILE
// on the volume (e.g. /data/melt/roadtrip.jsonl). The file is rotated to
// <path>.1 when it passes meltFileMax; one old generation is kept, so the disk
// holds at most about twice that. The same guard as the Redis sink applies:
// a record is refused in publish before it reaches either sink.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	meltFileMax   = 2 << 20 // bytes before the file is rotated to .1
	meltReadMax   = 1000    // records one read may return
	meltReadDflt  = 200
	meltEventName = 64
)

// appendFile writes one record as one line. Called only from drain.
func (m *meltSink) appendFile(body []byte) error {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(m.file), 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(m.file); err == nil && fi.Size()+int64(len(body))+1 > meltFileMax {
		if err := os.Rename(m.file, m.file+".1"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(m.file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(append([]byte{}, body...), '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readFile returns up to n records, newest first, from the file and then its
// .1 generation, keeping only those that match stream ("" for all) and event
// ("" for any). A line that does not parse (a torn last line) is skipped.
func (m *meltSink) readFile(stream, event string, n int) []map[string]any {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	out := []map[string]any{}
	for _, p := range []string{m.file, m.file + ".1"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var lines [][]byte
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			lines = append(lines, append([]byte{}, sc.Bytes()...))
		}
		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			var rec map[string]any
			if json.Unmarshal(lines[i], &rec) != nil {
				continue
			}
			if stream != "" && rec["stream"] != stream {
				continue
			}
			if event != "" && rec["event"] != event {
				continue
			}
			out = append(out, rec)
		}
		if len(out) >= n {
			break
		}
	}
	return out
}

// meltRead: GET /driver/melt.json?stream=events|errors|all&n=200&event=<name>,
// signed-in only. With the file sink off it says so and holds no records.
func (a *App) meltRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	stream := q.Get("stream")
	switch stream {
	case "", "all":
		stream = ""
	case "events", "errors":
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "stream is events, errors or all"})
		return
	}
	n := meltReadDflt
	if v, err := strconv.Atoi(q.Get("n")); err == nil && v > 0 {
		n = min(v, meltReadMax)
	}
	event := strings.TrimSpace(q.Get("event"))
	if len(event) > meltEventName {
		event = event[:meltEventName]
	}
	if a.melt == nil || a.melt.file == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"sink": "off", "records": []any{}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"sink": "file", "records": a.melt.readFile(stream, event, n)})
}
