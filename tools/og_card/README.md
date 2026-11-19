# og_card

Bakes `static/img/og-card.png`, the fallback link-preview card: 1200 by 630 PNG,
the masthead ground, Odo on his disc, "Road Trip" and his line. The colours are
read from `static/css/app.css` and Odo from `static/img/creature.svg` at bake
time. Contract: `spec-roadtrip-social-preview-cards`.

The bake is deterministic: fonts are passed by file (the Go family inside the
`golang.org/x/image` module) and system fonts are never loaded.

## Re-bake

From the repo root. The gate image has Go and node but no npm, so the
dependency is installed once in a node image, then the bake runs in the gate
image:

```bash
docker run --rm -v "$PWD/tools/og_card":/w -w /w node:20-slim npm ci --ignore-scripts --no-audit --no-fund
T="$(docker images roadtrip-gate --format '{{.Repository}}:{{.Tag}}' | head -n 1)"
docker run --rm -v "$PWD":"$PWD" -v roadtrip-gomod:/go/pkg/mod -w "$PWD" "$T" sh -c 'FONT_DIR=$(go list -m -f "{{.Dir}}" golang.org/x/image)/font/gofont/ttfs node tools/og_card/bake.mjs'
```

Commit the new PNG with the change that caused it. `node_modules` is ignored.

## After a deploy

Facebook caches a card by address. When the card changes, press **Scrape
Again** for the site's address in the Facebook Sharing Debugger by hand.
