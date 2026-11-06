package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// Private overlay — the EagleLakeRocks dedications pattern.
//
// A checkpoint's street address (a home, a rented cabin) never enters the
// repository or the seed. It lives in $DATA_DIR/private.json on the volume,
// keyed by RIDE and then by checkpoint ord within that ride:
//
//	{"checkpoints": {"3": {"address": "…", "notes": "…"}},
//	 "rides": {"<slug>": {"checkpoints": {"2": {"address": "…"}}}}}
//
// The top-level "checkpoints" block is the shape the file had when the site
// held one ride, and it still means exactly that: it belongs to the FIRST
// ride (RIDE_SLUG) and to no other. Every later ride has its own block under
// "rides". An ord says nothing without its ride, so nothing here hands out an
// entry without being told which ride is asking (forRide).
//
// It is read on each admin request (the file is tiny) and rendered ONLY on
// admin pages — the post form's checkpoint select and the lodging screen.
// Never on /, /legs, /photos or /route.geojson. A missing file is the normal
// state for anyone who clones and runs this, not an error.

const privateFile = "private.json"

type privateCheckpoint struct {
	Address string `json:"address"`
	Notes   string `json:"notes"`
}

// privateRide is one ride's entries, keyed by checkpoint ord.
type privateRide struct {
	Checkpoints map[string]privateCheckpoint `json:"checkpoints"`
}

type privateData struct {
	// Checkpoints is the first ride's block (see above).
	Checkpoints map[string]privateCheckpoint `json:"checkpoints"`
	// Rides holds every other ride's block, by slug.
	Rides map[string]privateRide `json:"rides,omitempty"`
}

// loadPrivate returns the overlay, or an empty one when the file is absent.
func loadPrivate(dataDir string) (privateData, error) {
	var pd privateData
	raw, err := os.ReadFile(filepath.Join(dataDir, privateFile))
	if os.IsNotExist(err) {
		return pd, nil
	}
	if err != nil {
		return pd, err
	}
	if err := json.Unmarshal(raw, &pd); err != nil {
		return privateData{}, err
	}
	return pd, nil
}

// forRide returns the entries of ONE ride, and it is the only way to read an
// entry. firstRide is RIDE_SLUG: the ride the top-level block belongs to. A
// ride with no block of its own gets nothing — never another ride's.
func (pd privateData) forRide(slug, firstRide string) privateRide {
	if r, ok := pd.Rides[slug]; ok {
		return r
	}
	if slug == firstRide {
		return privateRide{Checkpoints: pd.Checkpoints}
	}
	return privateRide{}
}

// setRide replaces one ride's block and touches no other. The first ride's
// block stays where it has always been, at the top level, so a site with one
// ride keeps writing the file it always wrote.
func (pd *privateData) setRide(slug, firstRide string, cps map[string]privateCheckpoint) {
	if slug == firstRide {
		pd.Checkpoints = cps
		delete(pd.Rides, slug)
		return
	}
	if len(cps) == 0 {
		delete(pd.Rides, slug)
		return
	}
	if pd.Rides == nil {
		pd.Rides = map[string]privateRide{}
	}
	pd.Rides[slug] = privateRide{Checkpoints: cps}
}

// entries counts every entry in the overlay, across every ride.
func (pd privateData) entries() int {
	n := len(pd.Checkpoints)
	for _, r := range pd.Rides {
		n += len(r.Checkpoints)
	}
	return n
}

// addressByOrd flattens one ride's block to ord → address for the templates.
func (pr privateRide) addressByOrd() map[int]string {
	out := map[int]string{}
	for k, v := range pr.Checkpoints {
		if ord, err := strconv.Atoi(k); err == nil && v.Address != "" {
			out[ord] = v.Address
		}
	}
	return out
}

// byOrd flattens one ride's block to ord → the whole entry, for the one
// screen that EDITS it. addressByOrd stays what the post form uses: it
// deliberately exposes the address alone, because that is all that screen has
// business rendering.
func (pr privateRide) byOrd() map[int]privateCheckpoint {
	out := map[int]privateCheckpoint{}
	for k, v := range pr.Checkpoints {
		if ord, err := strconv.Atoi(k); err == nil {
			out[ord] = v
		}
	}
	return out
}

// savePrivate writes the overlay atomically — a temp file alongside it, then a
// rename. The overlay is re-read on EVERY admin request, so a half-written
// file would break the very pages that exist to show it; a rename within the
// directory is the one write a concurrent reader cannot catch mid-way. Mode
// 0600: this is the file the addresses live in.
func savePrivate(dataDir string, pd privateData) error {
	if pd.Checkpoints == nil {
		pd.Checkpoints = map[string]privateCheckpoint{}
	}
	raw, err := json.MarshalIndent(pd, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(dataDir, ".private-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// A no-op once the rename has happened; what it is really for is every
	// failure path below.
	defer os.Remove(name)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	// fsync before the rename: the volume survives a machine restart, and a
	// renamed-but-unflushed file would come back empty.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dataDir, privateFile))
}
