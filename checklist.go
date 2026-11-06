package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A trip's checklist.
//
// A trip may carry a checklist: staged steps and open questions the
// trip-keeper checks off as they succeed, each stamped with a time and a
// public note of what actually happened. The page is public and written to
// help the next person who has to do the same thing.
//
// Two kinds of text travel through here, and the difference is the whole
// design:
//
//   - PUBLIC: titles, details, questions, the outcome notes. Anyone reads them.
//   - PRIVATE: the document's `private` fields and the private note taken at
//     check-off. An identifier, a name, a value. Only the signed-in
//     trip-keeper reads them.
//
// The one seam that enforces it is Store.Checklist(slug, withPrivate): a read
// without private never selects the private columns, so an anonymous request
// does not hold the text it must not show. Every handler decides withPrivate
// in exactly one place, from the same session the rest of /admin uses.
//
// The document itself lives on the volume beside the trip file, at
// $DATA_DIR/trips/<slug>.checklist.json, and never in the repository:
// fixtures are synthetic, and guard_test.go fails on a checklist document
// anywhere else.

const checklistSuffix = ".checklist.json"

// maxChecklistBytes caps a pushed document; the whole body is held in memory
// to be parsed.
const maxChecklistBytes = 512 << 10

// Length caps on a single field. A checklist is prose, not storage.
const (
	maxChecklistTitle = 200
	maxChecklistText  = 4000
	maxChecklistNote  = 2000
)

// defaultDisclaimer stands in when a document gives none: the page never
// renders without one.
const defaultDisclaimer = "One traveller's notes, not advice. Rules and fees change: confirm every step with the agencies involved."

const (
	clStep     = "step"
	clQuestion = "question"
)

// checklistDoc is the pushed document. Unknown fields are refused at the
// door, so a misspelt key is an error the pusher sees and not text that
// quietly went missing.
type checklistDoc struct {
	// Synthetic marks a fixture. A real document does not carry it.
	Synthetic bool `json:"synthetic,omitempty"`
	// Ride optionally names the ride the checklist belongs to; the push's
	// ?ride= says the same thing, and with neither it is the active ride.
	Ride       string          `json:"ride,omitempty"`
	Title      string          `json:"title"`
	Intro      string          `json:"intro"`
	Disclaimer string          `json:"disclaimer"`
	Stages     []clDocStage    `json:"stages"`
	Questions  []clDocQuestion `json:"questions"`
	Sources    []clSource      `json:"sources"`
}

type clDocStage struct {
	ID    string      `json:"id"`
	Title string      `json:"title"`
	TZ    string      `json:"tz"`
	When  string      `json:"when"`
	Steps []clDocStep `json:"steps"`
}

type clDocStep struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Private string `json:"private"`
}

type clDocQuestion struct {
	ID      string `json:"id"`
	Group   string `json:"group"`
	Ask     string `json:"ask"`
	Why     string `json:"why"`
	Private string `json:"private"`
}

type clSource struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

var clIDRE = regexp.MustCompile(`^[a-z0-9]+([-_][a-z0-9]+)*$`)

func validItemID(s string) bool { return len(s) > 0 && len(s) <= 64 && clIDRE.MatchString(s) }

// hasVINShape reports whether s holds a token shaped like a vehicle
// identification number: exactly 17 ASCII letters and digits in a row, none
// of them I, O or Q, at least one a digit. It is deliberately a shape and not
// a checksum: a public field is refused on the resemblance, because the cost
// of a false refusal is rewording a sentence and the cost of a miss is an
// identifier on a public page.
func hasVINShape(s string) bool {
	isAlnum := func(c byte) bool {
		return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	start := -1
	for i := 0; i <= len(s); i++ {
		if i < len(s) && isAlnum(s[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if tok := s[start:i]; len(tok) == 17 {
				digit, ok := false, true
				for j := 0; j < len(tok); j++ {
					switch c := tok[j]; {
					case c >= '0' && c <= '9':
						digit = true
					case strings.IndexByte("IOQioq", c) >= 0:
						ok = false
					}
				}
				if ok && digit {
					return true
				}
			}
			start = -1
		}
	}
	return false
}

// vinRefusal is the error for a public field that holds a VIN-shaped token.
// It names the field and never the value: the refusal is itself a public
// surface (a response body, a redirect, a log line).
func vinRefusal(field string) error {
	return fmt.Errorf("%s holds a 17-character token shaped like a vehicle identification number; "+
		"that belongs in a private field, not a public one", field)
}

// parseChecklist reads a pushed document completely and says whether it may
// be stored. Every public string is checked for a VIN shape; a field is named
// by its position in the document, never by its content.
func parseChecklist(raw []byte) (*checklistDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc checklistDoc
	if err := dec.Decode(&doc); err != nil {
		msg := err.Error()
		// A decoder error can quote a key. It must not be able to quote an
		// identifier, however the document was mangled.
		if hasVINShape(msg) {
			msg = "the document could not be read"
		}
		return nil, errors.New(msg)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("there is something after the end of the document")
	}
	if doc.Ride != "" && !validSlug(doc.Ride) {
		return nil, errors.New("ride must be lowercase letters, digits and single dashes")
	}

	type field struct{ name, value string }
	var public []field
	text := func(name, value string, max int, required bool) error {
		if required && strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
		if len(value) > max {
			return fmt.Errorf("%s is longer than %d bytes", name, max)
		}
		public = append(public, field{name, value})
		return nil
	}
	private := func(name, value string) error {
		if len(value) > maxChecklistText {
			return fmt.Errorf("%s is longer than %d bytes", name, maxChecklistText)
		}
		return nil
	}
	if err := text("title", doc.Title, maxChecklistTitle, true); err != nil {
		return nil, err
	}
	if err := text("intro", doc.Intro, maxChecklistText, false); err != nil {
		return nil, err
	}
	if err := text("disclaimer", doc.Disclaimer, maxChecklistText, false); err != nil {
		return nil, err
	}

	stageIDs, stepIDs, questionIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	steps := 0
	for i, st := range doc.Stages {
		at := fmt.Sprintf("stages[%d]", i)
		public = append(public, field{at + ".id", st.ID})
		if !validItemID(st.ID) {
			return nil, fmt.Errorf("%s.id must be lowercase letters and digits joined by single dashes or underscores", at)
		}
		if stageIDs[st.ID] {
			return nil, fmt.Errorf("%s.id is used by an earlier stage", at)
		}
		stageIDs[st.ID] = true
		if err := text(at+".title", st.Title, maxChecklistTitle, true); err != nil {
			return nil, err
		}
		if err := text(at+".when", st.When, maxChecklistTitle, false); err != nil {
			return nil, err
		}
		// A stamp is stored as UTC plus the stage's zone, so a stage without a
		// real one cannot be checked off honestly.
		public = append(public, field{at + ".tz", st.TZ})
		if st.TZ == "" || st.TZ == "Local" {
			return nil, fmt.Errorf("%s.tz is missing: every stage needs an IANA zone", at)
		}
		if _, err := time.LoadLocation(st.TZ); err != nil {
			return nil, fmt.Errorf("%s.tz is not an IANA zone", at)
		}
		for j, sp := range st.Steps {
			sat := fmt.Sprintf("%s.steps[%d]", at, j)
			public = append(public, field{sat + ".id", sp.ID})
			if !validItemID(sp.ID) {
				return nil, fmt.Errorf("%s.id must be lowercase letters and digits joined by single dashes or underscores", sat)
			}
			if stepIDs[sp.ID] {
				return nil, fmt.Errorf("%s.id is used by an earlier step; a step id is unique in the whole document", sat)
			}
			stepIDs[sp.ID] = true
			if err := text(sat+".title", sp.Title, maxChecklistTitle, true); err != nil {
				return nil, err
			}
			if err := text(sat+".detail", sp.Detail, maxChecklistText, false); err != nil {
				return nil, err
			}
			if err := private(sat+".private", sp.Private); err != nil {
				return nil, err
			}
			steps++
		}
	}
	for i, q := range doc.Questions {
		at := fmt.Sprintf("questions[%d]", i)
		public = append(public, field{at + ".id", q.ID})
		if !validItemID(q.ID) {
			return nil, fmt.Errorf("%s.id must be lowercase letters and digits joined by single dashes or underscores", at)
		}
		if questionIDs[q.ID] {
			return nil, fmt.Errorf("%s.id is used by an earlier question", at)
		}
		questionIDs[q.ID] = true
		if err := text(at+".group", q.Group, maxChecklistTitle, false); err != nil {
			return nil, err
		}
		if err := text(at+".ask", q.Ask, maxChecklistText, true); err != nil {
			return nil, err
		}
		if err := text(at+".why", q.Why, maxChecklistText, false); err != nil {
			return nil, err
		}
		if err := private(at+".private", q.Private); err != nil {
			return nil, err
		}
	}
	if steps == 0 && len(doc.Questions) == 0 {
		return nil, errors.New("a checklist needs at least one step or one question")
	}
	for i, s := range doc.Sources {
		at := fmt.Sprintf("sources[%d]", i)
		if err := text(at+".label", s.Label, maxChecklistTitle, true); err != nil {
			return nil, err
		}
		if err := text(at+".url", s.URL, 2000, true); err != nil {
			return nil, err
		}
		// A source is rendered as a link, so it is a web address and nothing else.
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%s.url must be an http or https address", at)
		}
	}
	for _, f := range public {
		if hasVINShape(f.value) {
			return nil, vinRefusal(f.name)
		}
	}
	return &doc, nil
}

func checklistPath(dataDir, slug string) string {
	return filepath.Join(dataDir, tripsDir, slug+checklistSuffix)
}

// stageChecklist writes a document beside its final name, the way stageTrip
// does: commit is the rename, discard removes the temp file. Mode 0600: this
// is a file that holds private text.
func stageChecklist(dataDir, slug string, raw []byte) (commit func() error, discard func(), err error) {
	if !validSlug(slug) {
		return nil, nil, fmt.Errorf("%q is not a ride slug", slug)
	}
	dir := filepath.Join(dataDir, tripsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp(dir, ".checklist-*.tmp")
	if err != nil {
		return nil, nil, err
	}
	name := tmp.Name()
	discard = func() { os.Remove(name) }
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		discard()
		return nil, nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		discard()
		return nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		discard()
		return nil, nil, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		discard()
		return nil, nil, err
	}
	commit = func() error { return os.Rename(name, checklistPath(dataDir, slug)) }
	return commit, discard, nil
}

// loadVolumeChecklists is the boot half, for a fresh machine or a restored
// volume: every checklist file on the volume goes through the same path a
// push takes. It is deploy-coupled like the trips above it, it never stamps
// or clears anything, and a file that will not load is logged and skipped.
// The log carries a file name and counts, never a word of the document.
func loadVolumeChecklists(store *Store, dataDir string) (loaded, failed int) {
	names, _ := filepath.Glob(filepath.Join(dataDir, tripsDir, "*"+checklistSuffix))
	sort.Strings(names)
	for _, name := range names {
		slug := strings.TrimSuffix(filepath.Base(name), checklistSuffix)
		fail := func(why string) {
			failed++
			log.Printf("WARN checklist file %s not loaded: %s", filepath.Base(name), why)
		}
		if !validSlug(slug) {
			fail("the file name is not a ride slug")
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			fail("it could not be read")
			continue
		}
		doc, err := parseChecklist(raw)
		if err != nil {
			fail(err.Error())
			continue
		}
		if doc.Ride != "" && doc.Ride != slug {
			fail("the document names another ride")
			continue
		}
		if _, err := store.Ride(slug); err != nil {
			fail("its ride is not in the database")
			continue
		}
		if _, err := store.ApplyChecklist(slug, doc); err != nil {
			fail("it could not be applied")
			continue
		}
		loaded++
	}
	return loaded, failed
}

// --- the store ------------------------------------------------------------------

// ChecklistItem is one step or one question as a page sees it. Private and
// PrivateNote are empty unless the read asked for private text.
type ChecklistItem struct {
	RowID   int64  `json:"-"`
	Kind    string `json:"-"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	Done    bool   `json:"done"`
	DoneAt  string `json:"done_at,omitempty"`
	DoneTZ  string `json:"tz,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	// FormTZ is the zone a time typed into this item's form is read in: the
	// stage's zone for a step.
	FormTZ      string `json:"-"`
	Private     string `json:"private,omitempty"`
	PrivateNote string `json:"private_note,omitempty"`

	stageID, group string
}

type ChecklistStage struct {
	ID    string          `json:"id"`
	Title string          `json:"title"`
	TZ    string          `json:"tz"`
	When  string          `json:"when,omitempty"`
	Done  int             `json:"done"`
	Total int             `json:"total"`
	Steps []ChecklistItem `json:"steps"`
}

type ChecklistGroup struct {
	Name      string          `json:"group"`
	Questions []ChecklistItem `json:"questions"`
}

// ChecklistSummary is the n-of-m line: counts only, so it can be asked for on
// every page without loading a word of the checklist.
type ChecklistSummary struct {
	Done      int `json:"steps_done"`
	Total     int `json:"steps"`
	Answered  int `json:"questions_answered"`
	Questions int `json:"questions"`
}

type Checklist struct {
	Ride       string           `json:"ride"`
	Title      string           `json:"title"`
	Intro      string           `json:"intro,omitempty"`
	Disclaimer string           `json:"disclaimer"`
	Summary    ChecklistSummary `json:"summary"`
	Stages     []ChecklistStage `json:"stages"`
	Groups     []ChecklistGroup `json:"questions"`
	Sources    []clSource       `json:"sources"`
}

// Checklist reads a ride's checklist, or nil when it has none.
//
// withPrivate is the seam. Without it the private columns are not in the
// query at all, so the value this returns cannot leak what it was never
// given: there is no field to forget to blank and no template condition to
// get wrong.
func (s *Store) Checklist(slug string, withPrivate bool) (*Checklist, error) {
	cl := &Checklist{Ride: slug, Stages: []ChecklistStage{}, Groups: []ChecklistGroup{}, Sources: []clSource{}}
	var sources string
	err := s.db.QueryRow(`SELECT title,intro,disclaimer,sources FROM checklists WHERE ride_slug=?`, slug).
		Scan(&cl.Title, &cl.Intro, &cl.Disclaimer, &sources)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cl.Disclaimer == "" {
		cl.Disclaimer = defaultDisclaimer
	}
	_ = json.Unmarshal([]byte(sources), &cl.Sources)

	rows, err := s.db.Query(`SELECT stage_id,title,tz,when_text FROM checklist_stages
	  WHERE ride_slug=? ORDER BY ord,id`, slug)
	if err != nil {
		return nil, err
	}
	stageAt := map[string]int{}
	for rows.Next() {
		var st ChecklistStage
		if err := rows.Scan(&st.ID, &st.Title, &st.TZ, &st.When); err != nil {
			rows.Close()
			return nil, err
		}
		st.Steps = []ChecklistItem{}
		stageAt[st.ID] = len(cl.Stages)
		cl.Stages = append(cl.Stages, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	privateCols := `'',''`
	if withPrivate {
		privateCols = `private,private_note`
	}
	rows, err = s.db.Query(`SELECT id,kind,item_id,stage_id,grp,title,detail,done_at,done_tz,outcome,`+privateCols+`
	  FROM checklist_items WHERE ride_slug=? ORDER BY ord,id`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groupAt := map[string]int{}
	for rows.Next() {
		var it ChecklistItem
		if err := rows.Scan(&it.RowID, &it.Kind, &it.ID, &it.stageID, &it.group, &it.Title, &it.Detail,
			&it.DoneAt, &it.DoneTZ, &it.Outcome, &it.Private, &it.PrivateNote); err != nil {
			return nil, err
		}
		it.Done = it.DoneAt != ""
		// An outcome note is what happened. On an item that is open again it is
		// kept for the trip-keeper and shown to nobody else.
		if !it.Done && !withPrivate {
			it.Outcome = ""
		}
		switch it.Kind {
		case clStep:
			i, ok := stageAt[it.stageID]
			if !ok {
				continue // a step whose stage is gone has nowhere to render
			}
			it.FormTZ = cl.Stages[i].TZ
			cl.Stages[i].Steps = append(cl.Stages[i].Steps, it)
			cl.Stages[i].Total++
			cl.Summary.Total++
			if it.Done {
				cl.Stages[i].Done++
				cl.Summary.Done++
			}
		case clQuestion:
			i, ok := groupAt[it.group]
			if !ok {
				i = len(cl.Groups)
				groupAt[it.group] = i
				cl.Groups = append(cl.Groups, ChecklistGroup{Name: it.group})
			}
			cl.Groups[i].Questions = append(cl.Groups[i].Questions, it)
			cl.Summary.Questions++
			if it.Done {
				cl.Summary.Answered++
			}
		}
	}
	return cl, rows.Err()
}

// ChecklistSummary counts a ride's checklist; ok is false when it has none.
func (s *Store) ChecklistSummary(slug string) (sum ChecklistSummary, ok bool) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM checklists WHERE ride_slug=?`, slug).Scan(&n); err != nil || n == 0 {
		return sum, false
	}
	rows, err := s.db.Query(`SELECT i.kind, COUNT(*), COALESCE(SUM(i.done_at<>''),0) FROM checklist_items i
	  WHERE i.ride_slug=? AND (i.kind<>? OR EXISTS
	    (SELECT 1 FROM checklist_stages st WHERE st.ride_slug=i.ride_slug AND st.stage_id=i.stage_id))
	  GROUP BY i.kind`, slug, clStep)
	if err != nil {
		return sum, false
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var total, done int
		if rows.Scan(&kind, &total, &done) != nil {
			return sum, false
		}
		switch kind {
		case clStep:
			sum.Total, sum.Done = total, done
		case clQuestion:
			sum.Questions, sum.Answered = total, done
		}
	}
	return sum, true
}

// ChecklistCounts is what one push did, in numbers.
type ChecklistCounts struct {
	Created   bool `json:"created"`
	Stages    int  `json:"stages"`
	Steps     int  `json:"steps"`
	Questions int  `json:"questions"`
	// Added and Refreshed are open items written from the document. KeptDone
	// is every done step and answered question, none of which was touched;
	// Dropped is the open items the document no longer has.
	Added     int `json:"added"`
	Refreshed int `json:"refreshed"`
	KeptDone  int `json:"kept_done"`
	Dropped   int `json:"dropped"`
}

// ApplyChecklist puts a document into the database, under the rule planning
// data already follows: the document owns an item only while it is open.
//
//   - an open step or question takes the document's text;
//   - a done step or an answered question is not written at all: not its
//     text, not its stamp, not its notes, not its place;
//   - an item the document no longer has is dropped if open and kept if done.
//
// The header (title, intro, disclaimer, sources) and the stages are the
// document's, always: they carry no stamp to protect. A stage the document
// dropped is kept only while a done step still stands in it.
func (s *Store) ApplyChecklist(slug string, doc *checklistDoc) (n ChecklistCounts, err error) {
	sources := doc.Sources
	if sources == nil {
		sources = []clSource{}
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return n, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return n, err
	}
	defer tx.Rollback()

	var have int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM checklists WHERE ride_slug=?`, slug).Scan(&have); err != nil {
		return n, err
	}
	n.Created = have == 0
	if _, err := tx.Exec(`INSERT INTO checklists (ride_slug,title,intro,disclaimer,sources) VALUES (?,?,?,?,?)
	  ON CONFLICT(ride_slug) DO UPDATE SET title=excluded.title, intro=excluded.intro,
	    disclaimer=excluded.disclaimer, sources=excluded.sources`,
		slug, doc.Title, doc.Intro, doc.Disclaimer, string(sourcesJSON)); err != nil {
		return n, err
	}

	type existing struct {
		row  int64
		done bool
	}
	current := map[string]existing{}
	rows, err := tx.Query(`SELECT id,kind,item_id,done_at<>'' FROM checklist_items WHERE ride_slug=?`, slug)
	if err != nil {
		return n, err
	}
	for rows.Next() {
		var e existing
		var kind, id string
		if err := rows.Scan(&e.row, &kind, &id, &e.done); err != nil {
			rows.Close()
			return n, err
		}
		current[kind+"/"+id] = e
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return n, err
	}

	seen := map[string]bool{}
	ord := 0
	put := func(kind, id, stage, group, title, detail, private string) error {
		ord++
		key := kind + "/" + id
		seen[key] = true
		e, ok := current[key]
		switch {
		case ok && e.done:
			n.KeptDone++
			return nil
		case ok:
			n.Refreshed++
			_, err := tx.Exec(`UPDATE checklist_items SET stage_id=?, grp=?, ord=?, title=?, detail=?, private=?
			  WHERE id=? AND done_at=''`, stage, group, ord, title, detail, private, e.row)
			return err
		default:
			n.Added++
			_, err := tx.Exec(`INSERT INTO checklist_items (ride_slug,kind,item_id,stage_id,grp,ord,title,detail,private)
			  VALUES (?,?,?,?,?,?,?,?,?)`, slug, kind, id, stage, group, ord, title, detail, private)
			return err
		}
	}
	stageSeen := map[string]bool{}
	for i, st := range doc.Stages {
		stageSeen[st.ID] = true
		if _, err := tx.Exec(`INSERT INTO checklist_stages (ride_slug,stage_id,ord,title,tz,when_text) VALUES (?,?,?,?,?,?)
		  ON CONFLICT(ride_slug,stage_id) DO UPDATE SET ord=excluded.ord, title=excluded.title,
		    tz=excluded.tz, when_text=excluded.when_text`, slug, st.ID, i+1, st.Title, st.TZ, st.When); err != nil {
			return n, err
		}
		n.Stages++
		for _, sp := range st.Steps {
			if err := put(clStep, sp.ID, st.ID, "", sp.Title, sp.Detail, sp.Private); err != nil {
				return n, err
			}
			n.Steps++
		}
	}
	for _, q := range doc.Questions {
		if err := put(clQuestion, q.ID, "", q.Group, q.Ask, q.Why, q.Private); err != nil {
			return n, err
		}
		n.Questions++
	}
	for key, e := range current {
		if seen[key] {
			continue
		}
		if e.done {
			n.KeptDone++
			continue
		}
		if _, err := tx.Exec(`DELETE FROM checklist_items WHERE id=? AND done_at=''`, e.row); err != nil {
			return n, err
		}
		n.Dropped++
	}
	// A stage the document no longer has goes, unless a done step still stands
	// in it: that step has to have somewhere to be shown.
	srows, err := tx.Query(`SELECT stage_id FROM checklist_stages WHERE ride_slug=?`, slug)
	if err != nil {
		return n, err
	}
	var gone []string
	for srows.Next() {
		var id string
		if err := srows.Scan(&id); err != nil {
			srows.Close()
			return n, err
		}
		if !stageSeen[id] {
			gone = append(gone, id)
		}
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return n, err
	}
	for _, id := range gone {
		if _, err := tx.Exec(`DELETE FROM checklist_stages WHERE ride_slug=? AND stage_id=? AND NOT EXISTS
		  (SELECT 1 FROM checklist_items WHERE ride_slug=? AND kind=? AND stage_id=?)`,
			slug, id, slug, clStep, id); err != nil {
			return n, err
		}
	}
	return n, tx.Commit()
}

// checklistRow is one item as the check-off handler needs it: what it is, the
// zone its stage is in, and what it holds now.
type checklistRow struct {
	RowID                int64
	Kind, ID             string
	StageTZ              string
	DoneAt, DoneTZ       string
	Outcome, PrivateNote string
}

func (s *Store) checklistRow(slug, kind, id string) (*checklistRow, error) {
	r := checklistRow{Kind: kind, ID: id}
	err := s.db.QueryRow(`SELECT i.id, COALESCE(st.tz,''), i.done_at, i.done_tz, i.outcome, i.private_note
	  FROM checklist_items i
	  LEFT JOIN checklist_stages st ON st.ride_slug=i.ride_slug AND st.stage_id=i.stage_id AND i.kind=?
	  WHERE i.ride_slug=? AND i.kind=? AND i.item_id=?`, clStep, slug, kind, id).
		Scan(&r.RowID, &r.StageTZ, &r.DoneAt, &r.DoneTZ, &r.Outcome, &r.PrivateNote)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SetChecklistItem writes one item's stamp and notes. An empty doneAt is an
// open item; the notes are written as given either way.
func (s *Store) SetChecklistItem(rowID int64, doneAt, doneTZ, outcome, privateNote string) error {
	_, err := s.db.Exec(`UPDATE checklist_items SET done_at=?, done_tz=?, outcome=?, private_note=? WHERE id=?`,
		doneAt, doneTZ, outcome, privateNote, rowID)
	return err
}
