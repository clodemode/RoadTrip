// Bakes static/img/og-card.png: the fallback social card, 1200x630 PNG.
// Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does (wave W1). See README.md.
//
//   FONT_DIR=<golang.org/x/image>/font/gofont/ttfs node tools/og_card/bake.mjs
//
// Fonts are passed by file and system fonts are never loaded, so the same
// inputs give the same bytes on any machine. Colours are the masthead and
// leg-1 values read from static/css/app.css at bake time.
import { readFileSync, writeFileSync } from "node:fs";
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
