# roadtrip/LORE

**YAML is canonical here.** Every `<character>/<slug>.yaml` in this tree is the
source of truth for that LORE row; the DB_INTENT copy is *materialized* from it
by `lairs_lore_yaml_load(app='roadtrip')` (YAML→DB). Edit the YAML, then load —
never the other way round.

Rows home here when `app_scope='roadtrip'` (ruled 2026-09-08,
`spec-clode-roadtrip-becomes-its-own-app-scope-bloodride-retires-to-static-2026-09-08`).
Before that ruling roadtrip work was filed under `app_scope='bloodride'` and
landed in `bloodride/LORE/`; those rows were moved here in the same pass.

A slug may have exactly ONE home — a copy left behind in another app's LORE
tree trips the cross-home duplicate check in ABOUT's pre-commit.
