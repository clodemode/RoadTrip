// Runs the page's own static/js/map.js against a small stand-in for Leaflet and
// the page, and prints what a visitor would be looking at. Reads on stdin:
//   {data, tracks, size:{w,h}, steps:[...], probes:[[lat,lng],...]}
// data is window.RIDE, tracks is window.RIDE_TRACKS, size is what the map's
// box measures when the script first runs, and steps happen after that:
//   {do:'resize', w, h}  the box is measured again (the page finished laying out)
//   {do:'touch'} {do:'wheel'} {do:'press'}  a finger, a wheel turn or a mouse press on the map
//   {do:'drag'} {do:'zoom'}  the visitor really moves or zooms the map
// The stand-in does the real Web Mercator arithmetic for fitBounds, so "is this
// point inside what is on screen" is a computed fact, not a guess. It is not a
// browser: tile loading, paint order and popups are not modelled.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const i = JSON.parse(readFileSync(0, 'utf8'));
const cur = { w: i.size.w, h: i.size.h };
const MAXZ = 18;

const px = (lat, lng, z) => {
  const W = 256 * Math.pow(2, z), s = Math.sin(lat * Math.PI / 180);
  return { x: (lng + 180) / 360 * W, y: (0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI)) * W };
};

const listeners = {};          // map.on handlers
const domListeners = {};       // listeners on the #map element
const state = { center: null, zoom: 0, size: { ...cur }, fits: 0 };
const markers = [];
const layers = {};             // name -> group, from L.control.layers
let ourFit = false;

const fire = (ev) => (listeners[ev] || []).forEach((f) => f());

class Bounds {
  constructor(pts) { this.p = []; (pts || []).forEach((q) => this.p.push(q)); }
  extend(q) { this.p.push(q); return this; }
  isValid() { return this.p.length > 0; }
}

const group = () => {
  const g = { layers: [], addTo() { return g; }, addLayer(l) { g.layers.push(l); return g; }, clearLayers() { g.layers.forEach((l) => { const k = markers.indexOf(l); if (k >= 0) markers.splice(k, 1); }); g.layers.length = 0; return g; } };
  return g;
};
const chain = () => { const o = { addTo(t) { if (t && t.addLayer) t.addLayer(o); return o; }, bindPopup(h) { o.popup = h; return o; } }; return o; };

const map = {
  whenReady(f) { f(); },
  on(ev, f) { (listeners[ev] = listeners[ev] || []).push(f); return map; },
  invalidateSize() { state.size = { ...cur }; },
  getZoom() { return state.zoom; },
  latLngToLayerPoint(ll) { const a = Array.isArray(ll) ? ll : [ll.lat, ll.lng]; const p = px(a[0], a[1], state.zoom); return { x: p.x, y: p.y }; },
  layerPointToLatLng(p) { return { lat: 0, lng: 0 }; },
  fitBounds(b, o) {
    const pad = (o && o.padding) || [0, 0];
    const availW = state.size.w - 2 * pad[0], availH = state.size.h - 2 * pad[1];
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    b.p.forEach((q) => { const p = px(q[0], q[1], 0); minX = Math.min(minX, p.x); maxX = Math.max(maxX, p.x); minY = Math.min(minY, p.y); maxY = Math.max(maxY, p.y); });
    const dx = Math.max(maxX - minX, 1e-9), dy = Math.max(maxY - minY, 1e-9);
    // A box too small to hold the padding has no room to fit anything: the map
    // clamps to its maximum zoom (the comment in map.js describes seeing exactly
    // that), so what is on screen is a few streets and nothing of the trip.
    const z = (availW <= 0 || availH <= 0) ? MAXZ : Math.max(0, Math.min(MAXZ, Math.floor(Math.log2(Math.min(availW / dx, availH / dy)))));
    const was = JSON.stringify([state.center, state.zoom]);
    state.zoom = z;
    state.center = { x: (minX + maxX) / 2 * Math.pow(2, z), y: (minY + maxY) / 2 * Math.pow(2, z) };
    state.fits++;
    if (was !== JSON.stringify([state.center, state.zoom])) { fire('movestart'); fire('zoomstart'); }
    return map;
  },
};

const el = {
  dataset: { mode: 'ride' },
  addEventListener(ev, f) { (domListeners[ev] = domListeners[ev] || []).push(f); },
  _leafletMap: null,
};
// A control map.js builds for itself (the Listen Inn used to be one).
let customControls = 0;

const L = {
  map() { state.size = { ...cur }; return map; },
  tileLayer() { return chain(); },
  control: Object.assign(function () { return { addTo(m) { return this; } }; }, {
    scale() { return chain(); },
    layers(a, over) { Object.assign(layers, over); return chain(); },
  }),
  DomEvent: { disableClickPropagation() {}, disableScrollPropagation() {} },
  layerGroup: group,
  polyline() { return chain(); },
  circleMarker() { return chain(); },
  divIcon(o) { return o; },
  latLngBounds(pts) { return new Bounds(pts); },
  point(x, y) { return { x, y }; },
  marker(ll, o) {
    const m = chain();
    m.rec = { lat: ll[0], lng: ll[1], cls: (o.icon && o.icon.className) || '', iconSize: o.icon && o.icon.iconSize, iconAnchor: o.icon && o.icon.iconAnchor,
      z: o.zIndexOffset || 0, interactive: o.interactive !== false, html: o.icon && o.icon.html };
    const add = m.addTo;
    m.addTo = (t) => { add(t); markers.push(m); return m; };
    m.bindPopup = (h) => { m.rec.popup = h; return m; };
    return m;
  },
};
L.control = Object.assign(function (o) { customControls++; const c = { onAdd: null, addTo(m) { if (c.onAdd) c.onAdd(); return c; } }; return c; }, L.control);
// Leaflet turns a marker's anchor and size into a screen offset; the note's own
// marker is the one under test, so keep its anchor and size as given.
L.map = function () { state.size = { ...cur }; map._el = el; el._leafletMap = map; return map; };

const byId = { map: el };
let resizeCb = null;
const win = { listeners: {}, addEventListener(ev, f) { (win.listeners[ev] = win.listeners[ev] || []).push(f); } };
const ctx = {
  L, module: undefined, console, Math, JSON, Object, Array, String, Number, Date, isNaN, isFinite, parseFloat, parseInt, RegExp, Error,
  window: null, document: { getElementById: (id) => byId[id] || null, addEventListener() {}, hidden: false },
  ResizeObserver: class { constructor(cb) { resizeCb = cb; } observe() {} },
  setInterval() {}, setTimeout() {}, fetch: () => new Promise(() => {}),
};
ctx.window = Object.assign(ctx, { RIDE: i.data, RIDE_TRACKS: (i.tracks && i.tracks.tracks) ? i.tracks : undefined, addEventListener: win.addEventListener });
vm.createContext(ctx);
vm.runInContext(readFileSync(new URL('./nowplaying.js', import.meta.url), 'utf8'), ctx);
vm.runInContext(readFileSync(new URL('./map.js', import.meta.url), 'utf8'), ctx);

for (const s of i.steps || []) {
  if (s.do === 'resize') { cur.w = s.w; cur.h = s.h; if (resizeCb) resizeCb(); }
  else if (s.do === 'touch') (domListeners.touchstart || []).forEach((f) => f({}));
  else if (s.do === 'wheel') (domListeners.wheel || []).forEach((f) => f({}));
  else if (s.do === 'press') (domListeners.mousedown || []).forEach((f) => f({}));
  else if (s.do === 'drag') fire('dragstart');
  else if (s.do === 'zoom') fire('zoomstart');
}

const inView = (lat, lng) => {
  if (!state.center) return false;
  const p = px(lat, lng, state.zoom);
  return Math.abs(p.x - state.center.x) <= state.size.w / 2 && Math.abs(p.y - state.center.y) <= state.size.h / 2;
};
process.stdout.write(JSON.stringify({
  contains: (i.probes || []).map((q) => inView(q[0], q[1])),
  zoom: state.zoom, fits: state.fits,
  // The Listen Inn list as the card draws it (listeninn.js calls this same function).
  panel: ctx.RTNowPlaying.panelHtml((i.tracks && i.tracks.tracks) || [], (s) => String(s == null ? '' : s), false, true),
  customControls,
  layerNames: Object.keys(layers),
  markers: markers.map((m) => ({ ...m.rec, inView: inView(m.rec.lat, m.rec.lng) })),
}));
