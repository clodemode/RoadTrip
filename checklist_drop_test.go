package main

import "testing"

// A volume from before the checklist was purged still has its three tables;
// the next boot's migration drops them, and running it again is a no-op.
func TestMigrationDropsTheOldChecklistTables(t *testing.T) {
	app := testApp(t)
	old := []string{"checklists", "checklist_stages", "checklist_items"}
	for _, tb := range old {
		if _, err := app.store.db.Exec(`CREATE TABLE ` + tb + ` (id INTEGER)`); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(tb string) bool {
		var n int
		if err := app.store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tb).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	for _, tb := range old {
		if !exists(tb) {
			t.Fatalf("%s was not created; the check below would pass on nothing", tb)
		}
	}
	for i := 0; i < 2; i++ {
		if err := app.store.migrate(); err != nil {
			t.Fatal(err)
		}
		for _, tb := range old {
			if exists(tb) {
				t.Errorf("run %d: %s is still there", i+1, tb)
			}
		}
	}
}
