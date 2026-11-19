// Bakes static/img/og-card.png, the fallback social card (1200x630 PNG), and the
// layers the binary draws a trip's card from: cardlayers/odo.png, cardlayers/horizon.png
// and cardlayers/tokens.json (waves W1 and W2).
// Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does. See README.md.
//
//   FONT_DIR=<golang.org/x/image>/font/gofont/ttfs node tools/og_card/bake.mjs
//
// Fonts are passed by file and system fonts are never loaded, so the same
// inputs give the same bytes on any machine. Colours are the masthead and
// leg-1 values read from static/css/app.css at bake time.
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createRequire } from "node:module";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..");
const { Resvg } = createRequire(import.meta.url)("@resvg/resvg-js");

const fontDir = process.env.FONT_DIR;
if (!fontDir) throw new Error("FONT_DIR must name the Go font folder (golang.org/x/image/font/gofont/ttfs)");
const fontFiles = ["Go-Bold.ttf", "Go-Medium.ttf", "Go-Regular.ttf"].map((f) => join(fontDir, f));

const css = readFileSync(join(root, "static", "css", "app.css"), "utf8");
const token = (name) => {
  const m = css.match(new RegExp("--" + name + ":\\s*(#[0-9a-fA-F]{3,8})"));
  if (!m) throw new Error("app.css has no --" + name);
  return m[1];
};
const C = {
  from: token("rt-mast-from"), mid: token("rt-mast-mid"), to: token("rt-mast-to"),
  edge: token("rt-mast-edge"), ink: token("rt-mast-ink"), tag: token("rt-mast-tag"),
  disc: token("leg-disc"), // first match: the default (leg 1) block
};

// Odo, still: the shapes of creature.svg without its <style> (a raster has no animation).
const odoInner = readFileSync(join(root, "static", "img", "creature.svg"), "utf8")
  .replace(/<style>[\s\S]*?<\/style>/, "")
  .replace(/<title>.*?<\/title>/, "")
  .replace(/<svg[^>]*>/, "")
  .replace(/<\/svg>\s*$/, "");

const SIZE = 420;
const S = SIZE / 64;
const DISC = { x: 920, y: 300, r: 245 };
// Odo's shapes sit between x 1..63 and y 19..57 of his 64 box: centre them on the disc.
const ox = DISC.x - 32 * S;
const oy = DISC.y - 38 * S;

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630" viewBox="0 0 1200 630">
  <defs><linearGradient id="mast" x1="0" y1="0" x2="1" y2="0.55">
    <stop offset="0" stop-color="${C.from}"/><stop offset="0.55" stop-color="${C.mid}"/><stop offset="1" stop-color="${C.to}"/>
  </linearGradient></defs>
  <rect width="1200" height="630" fill="url(#mast)"/>
  <rect y="618" width="1200" height="12" fill="${C.edge}"/>
  <circle cx="${DISC.x}" cy="${DISC.y}" r="${DISC.r}" fill="${C.disc}"/>
  <circle cx="${DISC.x}" cy="${DISC.y}" r="${DISC.r}" fill="none" stroke="${C.edge}" stroke-opacity=".35" stroke-width="6"/>
  <g transform="translate(${ox} ${oy}) scale(${S})">${odoInner}</g>
  <text x="72" y="150" font-family="Go" font-weight="700" font-size="26" letter-spacing="7" fill="${C.tag}">ROAD TRIP</text>
  <text x="66" y="276" font-family="Go" font-weight="700" font-size="108" letter-spacing="-2" fill="${C.ink}">Road Trip</text>
  <text x="72" y="350" font-family="Go Medium" font-size="44" fill="${C.ink}">Live from the road.</text>
  <text x="72" y="556" font-family="Go Medium" font-size="32" fill="${C.tag}">Odo counts every mile.</text>
</svg>`;

const png = new Resvg(svg, {
  fitTo: { mode: "width", value: 1200 },
  font: { fontFiles, loadSystemFonts: false, defaultFontFamily: "Go" },
}).render().asPng();

const out = join(root, "static", "img", "og-card.png");
writeFileSync(out, png);
console.log(`baked static/img/og-card.png (${png.length} bytes)`);

// ---- the layers a trip's card is drawn on (cardlayers/, embedded by the binary) ----
// Leg 1 is the ride's overall look, so its tokens and its horizon are the card's.
const layerDir = join(root, "cardlayers");
mkdirSync(layerDir, { recursive: true });
const render = (svgText, width) =>
  new Resvg(svgText, { fitTo: { mode: "width", value: width }, font: { loadSystemFonts: false } }).render().asPng();

const T = ["t1", "t2", "t3", "t4"].map(token);
const tokens = {
  legBg: token("leg-bg"), legBg2: token("leg-bg-2"), legInk: token("leg-ink"),
  legMuted: token("leg-muted"), legAccent: token("leg-accent"),
  // the masthead, for the strip of a photo card (wave W3)
  mastFrom: C.from, mastMid: C.mid, mastTo: C.to, mastEdge: C.edge, mastInk: C.ink, mastTag: C.tag, legDisc: C.disc,
};
writeFileSync(join(layerDir, "tokens.json"), JSON.stringify(tokens, null, 2) + "\n");

// Odo without his road and without his animation, cropped to the shapes (y 16 to 60 of his 64 box).
const odoStill = odoInner.replace(/<path id="road"[^>]*\/>/, "");
const odo = render(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="275" viewBox="0 16 64 44">${odoStill}</svg>`, 400);
writeFileSync(join(layerDir, "odo.png"), odo);

// The leg 1 horizon from templates/terrain.html: the final (default) <svg>, its var(--tN) fills resolved.
const terrain = readFileSync(join(root, "templates", "terrain.html"), "utf8");
const m = terrain.match(/<svg class="terrain" id="terrain-hudson-bend-to-cloudcroft"[^>]*>([\s\S]*?)<\/svg>/);
if (!m) throw new Error("templates/terrain.html has no leg 1 terrain");
const shapes = m[1].replace(/var\(--t([1-4])\)/g, (_, n) => T[Number(n) - 1]);
const horizon = render(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="210" viewBox="0 0 1000 200" preserveAspectRatio="none">${shapes}</svg>`, 1200);
writeFileSync(join(layerDir, "horizon.png"), horizon);
console.log(`baked cardlayers/odo.png (${odo.length} bytes), cardlayers/horizon.png (${horizon.length} bytes), cardlayers/tokens.json`);
