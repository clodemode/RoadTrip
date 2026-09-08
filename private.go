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
// keyed by checkpoint ord within the ride:
//
//	{"checkpoints": {"3": {"address": "…", "notes": "…"}}}
//
// It is read on each admin request (the file is tiny) and rendered ONLY on
// admin pages — the post form's checkpoint select. Never on /, /legs,
// /photos or /route.geojson. A missing file is the normal state for anyone
// who clones and runs this, not an error.

const privateFile = "private.json"

type privateCheckpoint struct {
	Address string `json:"address"`
	Notes   string `json:"notes"`
}

type privateData struct {
	Checkpoints map[string]privateCheckpoint `json:"checkpoints"`
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
		return pd, err
	}
	return pd, nil
}

// addressByOrd flattens the overlay to ord → address for the templates.
func (pd privateData) addressByOrd() map[int]string {
	out := map[int]string{}
	for k, v := range pd.Checkpoints {
		if ord, err := strconv.Atoi(k); err == nil && v.Address != "" {
			out[ord] = v.Address
		}
	}
	return out
}

// byOrd flattens the overlay to ord → the whole entry, for the one screen that
// EDITS it. addressByOrd stays what the post form uses: it deliberately
// exposes the address alone, because that is all that screen has business
// rendering.
func (pd privateData) byOrd() map[int]privateCheckpoint {
	out := map[int]privateCheckpoint{}
	for k, v := range pd.Checkpoints {
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
