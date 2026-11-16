package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// STACKED SETUP. What every test App needs is built in layers, and each layer
// is paid once at the widest scope it is true for:
//
//	layer 1, once per test process   the password cost (testmain_test.go)
//	layer 2, once per test process   a store with the schema created, every
//	                                 migration run and the embedded ride seeded:
//	                                 kept as the bytes of one SQLite file
//	layer 3, once per App            a private copy of those bytes, opened
//
// Layer 3 is what keeps scenarios alone: no App ever opens the layer-2 file,
// only its own copy, so nothing one scenario writes can reach another. The
// feature stacked_setup_isolation.feature proves it with a scenario that
// changes its store and a neighbour that must not see the change.
//
// Before this, every App created the schema, ran the migrations and parsed and
// inserted the whole ride again, several times per scenario.

var seedLayer struct {
	once sync.Once
	db   []byte
	slug string
	err  error
}

// buildSeedLayer makes layer 2: open, seed from the geojson the binary embeds
// (so fixture and production agree by construction), close, keep the bytes.
func buildSeedLayer() ([]byte, string, error) {
	dir, err := os.MkdirTemp("", "roadtrip-seed-layer-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "seed.sqlite3")
	store, err := OpenStore(path)
	if err != nil {
		return nil, "", fmt.Errorf("open store: %w", err)
	}
	names, err := fs.Glob(seedFS, "seed/*.geojson")
	if err != nil || len(names) == 0 {
		store.Close()
		return nil, "", fmt.Errorf("no embedded seed: %v", err)
	}
	raw, err := seedFS.ReadFile(names[0])
	if err != nil {
		store.Close()
		return nil, "", fmt.Errorf("read seed: %w", err)
	}
	staged := filepath.Join(dir, "seed.geojson")
	if err := os.WriteFile(staged, raw, 0o600); err != nil {
		store.Close()
		return nil, "", fmt.Errorf("stage seed: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(names[0]), filepath.Ext(names[0]))
	slug, _, err := store.Seed(staged, base)
	if err != nil {
		store.Close()
		return nil, "", fmt.Errorf("seed: %w", err)
	}
	// Fold the write-ahead log into the one file before it is copied.
	if _, err := store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		store.Close()
		return nil, "", fmt.Errorf("checkpoint: %w", err)
	}
	if err := store.Close(); err != nil {
		return nil, "", fmt.Errorf("close: %w", err)
	}
	db, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(db) == 0 {
		return nil, "", fmt.Errorf("the seed layer is an empty file")
	}
	return db, slug, nil
}

// seededStore is layer 3: a private copy of the seed layer at path, opened.
// It returns the store and the slug of the ride it holds.
func seededStore(t *testing.T, path string) (*Store, string) {
	t.Helper()
	seedLayer.once.Do(func() { seedLayer.db, seedLayer.slug, seedLayer.err = buildSeedLayer() })
	if seedLayer.err != nil {
		t.Fatalf("seed layer: %v", seedLayer.err)
	}
	if err := os.WriteFile(path, seedLayer.db, 0o600); err != nil {
		t.Fatalf("copy the seed layer: %v", err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store, seedLayer.slug
}
