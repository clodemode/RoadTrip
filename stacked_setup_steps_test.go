package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// stackWorld binds features/stacked_setup_isolation.feature.
type stackWorld struct {
	t      *testing.T
	app    *App
	second *App
}

func initStackedSetupScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &stackWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = stackWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a site built from the stacked setup$`, func() { w.app = testApp(w.t) })
	sc.Step(`^a second site built from the stacked setup$`, func() { w.second = testApp(w.t) })
	sc.Step(`^every checkpoint in its store is renamed "([^"]*)"$`, w.renameAll)
	sc.Step(`^its store holds a checkpoint named "([^"]*)"$`, func(name string) error { return holds(w.app, name, true) })
	sc.Step(`^its store holds no checkpoint named "([^"]*)"$`, func(name string) error { return holds(w.app, name, false) })
	sc.Step(`^the second site's store holds no checkpoint named "([^"]*)"$`, func(name string) error { return holds(w.second, name, false) })
	sc.Step(`^its store holds the same checkpoints as a store seeded from nothing$`, w.sameAsFromNothing)
}

func (w *stackWorld) renameAll(name string) error {
	res, err := w.app.store.db.Exec(`UPDATE checkpoints SET name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("the store holds no checkpoint to rename, so the scenario proves nothing")
	}
	return nil
}

func checkpointNames(s *Store) ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM checkpoints ORDER BY leg_id, ord, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func holds(app *App, name string, want bool) error {
	names, err := checkpointNames(app.store)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("the store holds no checkpoint at all")
	}
	found := false
	for _, n := range names {
		found = found || n == name
	}
	if found != want {
		return fmt.Errorf("a checkpoint named %q: found %v, want %v", name, found, want)
	}
	return nil
}

// sameAsFromNothing seeds a store the long way, with no layer under it, and
// compares: the copy every site gets is what a fresh seed would have made.
func (w *stackWorld) sameAsFromNothing() error {
	dir := w.t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "fresh.sqlite3"))
	if err != nil {
		return err
	}
	defer store.Close()
	names, err := fs.Glob(seedFS, "seed/*.geojson")
	if err != nil || len(names) == 0 {
		return fmt.Errorf("no embedded seed: %v", err)
	}
	raw, err := seedFS.ReadFile(names[0])
	if err != nil {
		return err
	}
	staged := filepath.Join(dir, "seed.geojson")
	if err := os.WriteFile(staged, raw, 0o600); err != nil {
		return err
	}
	if _, _, err := store.Seed(staged, strings.TrimSuffix(filepath.Base(names[0]), filepath.Ext(names[0]))); err != nil {
		return err
	}
	fresh, err := checkpointNames(store)
	if err != nil {
		return err
	}
	mine, err := checkpointNames(w.app.store)
	if err != nil {
		return err
	}
	if len(fresh) == 0 || strings.Join(fresh, "\n") != strings.Join(mine, "\n") {
		return fmt.Errorf("the stacked store holds %d checkpoints, a fresh seed %d, or their names differ", len(mine), len(fresh))
	}
	return nil
}
