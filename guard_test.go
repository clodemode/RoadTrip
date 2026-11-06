package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The repository is public and a trip is not. Three things keep a trip out of
// it, and this file is the test that they are all still there:
//
//  1. .gitignore and .dockerignore each carry a line for the data dir and one
//     for the planning-handoff folder, so neither can be staged by accident
//     nor copied into an image build.
//  2. No file git knows about — tracked, or untracked and not ignored — is a
//     trip document, except the one seed the image has always carried and the
//     fixtures under testdata/.
//  3. A fixture under testdata/ says it is synthetic.
//
// It fails closed: if it cannot list the files, or lists none, that is a
// failure and not a pass.

// seedAllowlist is every trip document the repository may hold outside
// testdata/. It is a list, not a pattern: a new file under seed/ is a new
// trip in a public repository, and has to be put here on purpose.
var seedAllowlist = map[string]bool{
	"seed/atx-sjc-2026-09.geojson": true,
}

// guardLines is what each ignore file must contain, as whole lines.
var guardLines = map[string][]string{
	".gitignore":    {"data/", "coWork_input/"},
	".dockerignore": {"data/", "coWork_input/"},
}

// privateDirs may hold nothing git knows about.
var privateDirs = []string{"data/", "coWork_input/"}

// isTripDocument recognises a trip by its shape, whatever the file is called:
// GeoJSON features with a line or a point in them, or a planning tool's route
// document.
func isTripDocument(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	var doc struct {
		Schema   string            `json:"schema"`
		Stops    []json.RawMessage `json:"stops"`
		Segments []json.RawMessage `json:"segments"`
		Features []struct {
			Geometry struct {
				Type string `json:"type"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	if strings.HasPrefix(doc.Schema, routeSchemaPrefix) || (len(doc.Stops) > 0 && len(doc.Segments) > 0) {
		return true
	}
	for _, f := range doc.Features {
		switch f.Geometry.Type {
		case "LineString", "MultiLineString", "Point":
			return true
		}
	}
	return false
}

func isSynthetic(raw []byte) bool {
	var doc struct {
		Synthetic bool `json:"synthetic"`
	}
	return json.Unmarshal(raw, &doc) == nil && doc.Synthetic
}

// repoGuard checks one tree. files are slash-separated paths relative to
// root. It returns every violation, and an error only when it could not look.
func repoGuard(root string, files []string) ([]string, error) {
	var bad []string
	for name, want := range guardLines {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s is missing: %v", name, err))
			continue
		}
		have := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			have[strings.TrimSpace(line)] = true
		}
		for _, line := range want {
			if !have[line] {
				bad = append(bad, fmt.Sprintf("%s has no %q line", name, line))
			}
		}
	}
	if len(files) == 0 {
		return bad, fmt.Errorf("no files to check")
	}
	for _, f := range files {
		for _, dir := range privateDirs {
			if strings.HasPrefix(f, dir) {
				bad = append(bad, fmt.Sprintf("%s is under %s, which holds private data", f, dir))
			}
		}
		fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil || fi.IsDir() || fi.Size() > 32<<20 {
			continue // deleted in the work tree, a submodule, or far too big to be text
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return bad, fmt.Errorf("read %s: %w", f, err)
		}
		if !isTripDocument(raw) {
			continue
		}
		switch {
		case seedAllowlist[f]:
		case strings.HasPrefix(f, "testdata/"):
			if !isSynthetic(raw) {
				bad = append(bad, fmt.Sprintf("%s is a trip document under testdata/ that does not say \"synthetic\": true", f))
			}
		default:
			bad = append(bad, fmt.Sprintf("%s is a trip document outside the seed allowlist and testdata/", f))
		}
	}
	sort.Strings(bad)
	return bad, nil
}

// repoFiles lists what git knows about in the work tree: tracked files, and
// untracked files that are not ignored — the second being a trip document
// dropped in the tree that the next `git add` would take. In a tree that is
// not a git checkout (a source tarball) it walks the directory instead.
func repoFiles(root string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		var out []string
		err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !fi.IsDir() {
				rel, _ := filepath.Rel(root, p)
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
		return out, err
	}
	// safe.directory: the tree is often mounted into a container whose user
	// does not own it, and git would otherwise refuse to look.
	cmd := exec.Command("git", "-c", "safe.directory=*", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []string
	for _, f := range strings.Split(string(raw), "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out, nil
}

func TestRepoHoldsNoTripDocument(t *testing.T) {
	files, err := repoFiles(".")
	if err != nil {
		t.Fatalf("could not list the repository's files, so nothing was checked: %v", err)
	}
	// The listing has to be a real one before its silence means anything.
	seen := map[string]bool{}
	for _, f := range files {
		seen[f] = true
	}
	for _, must := range []string{"main.go", "guard_test.go", "seed/atx-sjc-2026-09.geojson", "testdata/trip-fixture.geojson"} {
		if !seen[must] {
			t.Fatalf("the file listing (%d files) does not include %s; it is not a listing of this repository", len(files), must)
		}
	}
	bad, err := repoGuard(".", files)
	if err != nil {
		t.Fatalf("the guard could not look: %v", err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}

// The guard has to be able to fire. Each case plants one thing it exists to
// catch in a scratch tree and expects exactly that complaint.
func TestRepoGuardFires(t *testing.T) {
	trip, err := os.ReadFile(filepath.Join("testdata", "trip-fixture.geojson"))
	if err != nil {
		t.Fatal(err)
	}
	route, err := os.ReadFile(filepath.Join("testdata", "route-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	unmarked := bytes.Replace(trip, []byte(`"synthetic": true,`), nil, 1)
	if bytes.Equal(unmarked, trip) {
		t.Fatal("the fixture has no synthetic marker to remove")
	}
	ignore := "data/\ncoWork_input/\n"
	clean := map[string]string{".gitignore": ignore, ".dockerignore": ignore, "main.go": "package main\n",
		"seed/atx-sjc-2026-09.geojson": string(trip), "testdata/trip-fixture.geojson": string(trip),
		"testdata/route-fixture.json": string(route), "notes.json": `{"features": []}`}
	cases := []struct {
		name   string
		change func(tree map[string]string)
		want   string
	}{
		{"a clean tree", func(map[string]string) {}, ""},
		{"a trip at the root", func(m map[string]string) { m["my-trip.geojson"] = string(trip) }, "my-trip.geojson is a trip document"},
		{"a trip added to seed/", func(m map[string]string) { m["seed/next-trip.geojson"] = string(trip) }, "seed/next-trip.geojson is a trip document"},
		{"a trip under another name", func(m map[string]string) { m["docs/notes.txt"] = string(trip) }, "docs/notes.txt is a trip document"},
		{"a planning tool's route document", func(m map[string]string) { m["routes.json"] = string(route) }, "routes.json is a trip document"},
		{"a fixture that does not say it is synthetic", func(m map[string]string) { m["testdata/real.geojson"] = string(unmarked) }, "testdata/real.geojson is a trip document under testdata/"},
		{"anything under data/", func(m map[string]string) { m["data/private.json"] = "{}" }, "data/private.json is under data/"},
		{"anything under the handoff folder", func(m map[string]string) { m["coWork_input/readme.md"] = "x" }, "coWork_input/readme.md is under coWork_input/"},
		{"no handoff line in .gitignore", func(m map[string]string) { m[".gitignore"] = "data/\n" }, `.gitignore has no "coWork_input/" line`},
		{"no data line in .gitignore", func(m map[string]string) { m[".gitignore"] = "coWork_input/\n" }, `.gitignore has no "data/" line`},
		{"a commented-out guard line", func(m map[string]string) { m[".gitignore"] = "data/\n# coWork_input/\n" }, `.gitignore has no "coWork_input/" line`},
		{"no handoff line in .dockerignore", func(m map[string]string) { m[".dockerignore"] = "data/\n" }, `.dockerignore has no "coWork_input/" line`},
		{"no .gitignore at all", func(m map[string]string) { delete(m, ".gitignore") }, ".gitignore is missing"},
	}
	for _, c := range cases {
		tree := map[string]string{}
		for k, v := range clean {
			tree[k] = v
		}
		c.change(tree)
		root := t.TempDir()
		var files []string
		for name, body := range tree {
			p := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			files = append(files, name)
		}
		// The scratch tree is not a git checkout, so this is the walk.
		listed, err := repoFiles(root)
		if err != nil || len(listed) != len(files) {
			t.Fatalf("%s: the walk listed %d of %d files: %v", c.name, len(listed), len(files), err)
		}
		bad, err := repoGuard(root, listed)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		switch {
		case c.want == "" && len(bad) != 0:
			t.Errorf("%s: complained %v", c.name, bad)
		case c.want != "" && (len(bad) != 1 || !strings.Contains(bad[0], c.want)):
			t.Errorf("%s: got %v, want exactly one complaint containing %q", c.name, bad, c.want)
		}
	}
	// And a guard handed nothing to check says so rather than passing.
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(ignore), 0o644)
	_ = os.WriteFile(filepath.Join(root, ".dockerignore"), []byte(ignore), 0o644)
	if _, err := repoGuard(root, nil); err == nil {
		t.Error("a guard with no files to check reported no error")
	}
}

func TestIsTripDocument(t *testing.T) {
	yes := []string{
		`{"type":"FeatureCollection","features":[{"geometry":{"type":"LineString","coordinates":[]}}]}`,
		`{"features":[{"geometry":{"type":"Point","coordinates":[1,2]}}]}`,
		`{"schema":"roadtrip-route/0.1"}`,
		`{"stops":[{}],"segments":[{}]}`,
	}
	no := []string{``, `package main`, `[1,2,3]`, `{"hooks":{}}`, `{"features":[]}`, `{"checkpoints":{"3":{"address":"x"}}}`, `{ not json`}
	for _, s := range yes {
		if !isTripDocument([]byte(s)) {
			t.Errorf("not recognised as a trip document: %s", s)
		}
	}
	for _, s := range no {
		if isTripDocument([]byte(s)) {
			t.Errorf("wrongly recognised as a trip document: %s", s)
		}
	}
}
