// Runs the page's own static/js/map.js against a small stand-in for Leaflet and
// the page, and prints what a visitor would be looking at. Reads on stdin:
//   {data, tracks, size:{w,h}, steps:[...], probes:[[lat,lng],...]}
// data is window.RIDE, tracks is window.RIDE_TRACKS, size is what the map's
// box measures when the script first runs, and steps happen after that:
//   {do:'resize', w, h}  the box is measured again (the page finished laying out)
//   {do:'touch'} {do:'wheel'} {do:'press'}  a finger, a wheel turn or a mouse press on the map
//   {do:'drag'} {do:'zoom'}  the visitor really moves or zooms the map
//   {do:'finger', n}  n fingers move on the map (touchmove); {do:'wait', ms}  the clock runs
//   {do:'pan', dx, dy}  the view is moved by the visitor; {do:'tapCluster'}  a count is tapped
//   {do:'control', name}  a map button is pressed (fit | full | done); {do:'key', key}  a key is pressed
// Also read: touch (the screen is a coarse pointer), mode ('ride' | 'leg'). The stand-in groups
// the markers of an L.markerClusterGroup by pixel distance at the current zoom, as the
// vendored plugin does; the plugin's own arithmetic is not run here.
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
let mapOpts = null;            // what L.map was asked for
let invalidated = 0;
const trace = { opening: null };
const state = { center: null, zoom: 0, size: { ...cur }, fits: 0 };
const markers = [];            // every marker the page made; visible() says which are on the map
const lines = [];              // every polyline
const circles = [];            // every circle marker
const layers = {};             // name -> group, from L.control.layers
let ourFit = false;

const fire = (ev) => (listeners[ev] || []).forEach((f) => f());

class Bounds {
  constructor(pts) { this.p = []; (pts || []).forEach((q) => this.p.push(q)); }
  extend(q) { this.p.push(q); return this; }
  isValid() { return this.p.length > 0; }
}

// A layer is on the map when it sits on the map itself, or in a group that is on the map and holds it.
const visible = (o) => { const p = o.parent; return !!p && (p === map || (p.layers.indexOf(o) >= 0 && !!p.onMap)); };
const group = () => {
  const g = { layers: [], onMap: false, addTo(t) { g.parent = t; g.onMap = t === map; return g; }, addLayer(l) { l.parent = g; if (g.layers.indexOf(l) < 0) g.layers.push(l); return g; }, clearLayers() { g.layers.length = 0; return g; } };
  return g;
};
const gathers = [];            // every L.markerClusterGroup the page made
const clusterGroup = (opts) => {
  const g = group();
  g.opts = opts || {};
  g.gathering = true;
  g.removeLayers = (ls) => { ls.forEach((l) => { const k = g.layers.indexOf(l); if (k >= 0) g.layers.splice(k, 1); }); return g; };
  g.addLayers = (ls) => { ls.forEach((l) => g.addLayer(l)); return g; };
  gathers.push(g);
  return g;
};
// The groups of markers that overlap at the current zoom, as a count each.
const gatherNow = () => {
  const out = [];
  gathers.forEach((g) => {
    if (!g.onMap) return;
    const r = (g.opts.maxClusterRadius) || 80;
    const left = g.layers.slice();
    while (left.length) {
      const a = left.shift(), pa = px(a.rec.lat, a.rec.lng, state.zoom), mine = [a];
      for (let k = left.length - 1; k >= 0; k--) {
        const pb = px(left[k].rec.lat, left[k].rec.lng, state.zoom);
        if (Math.hypot(pa.x - pb.x, pa.y - pb.y) <= r) mine.push(left.splice(k, 1)[0]);
      }
      out.push({ count: mine.length, members: mine, group: g });
    }
  });
  return out;
};
const chain = () => { const o = { addTo(t) { o.parent = t; if (t && t.addLayer) t.addLayer(o); return o; }, bindPopup(h) { o.popup = h; return o; } }; return o; };

const map = {
  whenReady(f) { f(); },
  addLayer(g) { g.onMap = true; g.parent = map; return map; },
  removeLayer(g) { g.onMap = false; return map; },
  hasLayer(g) { return !!g.onMap; },
  on(ev, f) { (listeners[ev] = listeners[ev] || []).push(f); return map; },
  invalidateSize() { state.size = { ...cur }; invalidated++; },
  getContainer() { return el; },
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
    if (!(o && o.gather)) state.fits++;
    if (was !== JSON.stringify([state.center, state.zoom])) { fire('movestart'); fire('zoomstart'); }
    return map;
  },
};

// A small DOM: the page's own scripts build buttons and a hint with it.
const makeEl = (tag) => {
  const cls = new Set();
  const e = {
    tagName: tag, children: [], attrs: {}, listeners: {}, style: {}, hidden: false, textContent: '', innerHTML: '', title: '', dataset: {},
    classList: {
      add: (...c) => c.forEach((x) => cls.add(x)), remove: (...c) => c.forEach((x) => cls.delete(x)), contains: (x) => cls.has(x),
      toggle(c, on) { const want = on === undefined ? !cls.has(c) : !!on; if (want) cls.add(c); else cls.delete(c); return want; },
    },
    setAttribute(k, v) { e.attrs[k] = String(v); }, getAttribute(k) { return k in e.attrs ? e.attrs[k] : null }, removeAttribute(k) { delete e.attrs[k]; },
    appendChild(c) { e.children.push(c); c.parent = e; return c; }, append(...cs) { cs.forEach((c) => e.appendChild(c)); },
    addEventListener(ev, f) { (e.listeners[ev] = e.listeners[ev] || []).push(f); },
    querySelector() { return null; }, querySelectorAll() { return []; }, contains() { return false; },
    getBoundingClientRect() { return { left: 0, top: 0, width: cur.w, height: cur.h }; },
  };
  Object.defineProperty(e, 'className', { get() { return [...cls].join(' '); }, set(v) { cls.clear(); String(v).split(/\s+/).filter(Boolean).forEach((x) => cls.add(x)); } });
  return e;
};
const walk = (e, f) => { f(e); e.children.forEach((c) => walk(c, f)); };
const el = makeEl('div');
el.dataset.mode = i.mode || 'ride';
el._leafletMap = null;
const controlNodes = [];       // what each L.control built, with its corner
let customControls = 0;

const L = {
  map(id, o) { mapOpts = o || {}; state.size = { ...cur }; return map; },
  tileLayer() { return chain(); },
  control: Object.assign(function () { return { addTo(m) { return this; } }; }, {
    scale() { return chain(); },
    layers(a, over) { Object.assign(layers, over); return chain(); },
  }),
  DomEvent: { disableClickPropagation() {}, disableScrollPropagation() {} },
  layerGroup: group,
  markerClusterGroup: clusterGroup,
  polyline(pts, o) { const c = chain(); c.style = o || {}; lines.push(c); return c; },
  circleMarker() { const c = chain(); circles.push(c); return c; },
  divIcon(o) { return o; },
  latLngBounds(pts) { return new Bounds(pts); },
  point(x, y) { return { x, y }; },
  marker(ll, o) {
    const m = chain();
    m.rec = { lat: ll[0], lng: ll[1], cls: (o.icon && o.icon.className) || '', iconSize: o.icon && o.icon.iconSize, iconAnchor: o.icon && o.icon.iconAnchor,
      z: o.zIndexOffset || 0, interactive: o.interactive !== false, html: o.icon && o.icon.html };
    const add = m.addTo;
    markers.push(m);   // made; it is on the map only once a group or the map holds it (visible())
    m.addTo = (t) => { add(t); return m; };
    m.bindPopup = (h) => { m.rec.popup = h; return m; };
    m.getLatLng = () => ({ lat: ll[0], lng: ll[1] });
    return m;
  },
};
L.control = Object.assign(function (o) {
  const c = { onAdd: null, options: o || {}, addTo(m) {
    if (c.onAdd) {
      const n = c.onAdd(m);
      if (n) controlNodes.push({ node: n, position: c.options.position || 'topright' });
      // The Fit / Full screen bar is the shared helper's; any other control the script builds is counted.
      if (!n || !/\brt-map-bar\b/.test(n.className || '')) customControls++;
    }
    return c;
  } };
  return c;
}, L.control);
// Leaflet turns a marker's anchor and size into a screen offset; the note's own
// marker is the one under test, so keep its anchor and size as given.
L.map = function (id, o) { mapOpts = o || {}; state.size = { ...cur }; map._el = el; el._leafletMap = map; return map; };

const byId = { map: el };
let resizeCb = null;
const win = { listeners: {}, addEventListener(ev, f) { (win.listeners[ev] = win.listeners[ev] || []).push(f); } };
// The clock: a timer runs when a wait step reaches it.
let clock = 0;
const timers = [];
const advance = (ms) => {
  const to = clock + ms;
  for (;;) {
    timers.sort((a, b) => a.at - b.at);
    if (!timers.length || timers[0].at > to) break;
    const t = timers.shift(); clock = t.at; t.f();
  }
  clock = to;
};
const docEl = makeEl('document');
docEl.body = makeEl('body');
docEl.getElementById = (id) => byId[id] || null;
docEl.createElement = (tag) => makeEl(tag);
docEl.hidden = false;
const ctx = {
  L, module: undefined, console, Math, JSON, Object, Array, String, Number, Date, isNaN, isFinite, parseFloat, parseInt, RegExp, Error, Set,
  window: null, document: docEl,
  matchMedia: (q) => ({ matches: /pointer:\s*coarse/.test(q) ? !!i.touch : false }),
  ResizeObserver: class { constructor(cb) { resizeCb = cb; } observe() {} },
  setInterval() {}, setTimeout(f, ms) { const t = { f, at: clock + (ms || 0) }; timers.push(t); return t; },
  clearTimeout(t) { const k = timers.indexOf(t); if (k >= 0) timers.splice(k, 1); },
  fetch: () => new Promise(() => {}),
};
ctx.window = Object.assign(ctx, { RIDE: i.data, RIDE_TRACKS: (i.tracks && i.tracks.tracks) ? i.tracks : undefined, addEventListener: win.addEventListener });
vm.createContext(ctx);
vm.runInContext(readFileSync(new URL('./nowplaying.js', import.meta.url), 'utf8'), ctx);
// map_common.js is the one place the Ride, leg and Plan maps take their options and buttons from.
try { vm.runInContext(readFileSync(new URL('./map_common.js', import.meta.url), 'utf8'), ctx); } catch (e) { if (e.code !== 'ENOENT') throw e; }
vm.runInContext(readFileSync(new URL('./map.js', import.meta.url), 'utf8'), ctx);
const snap = () => ({ zoom: state.zoom, center: state.center ? { x: Math.round(state.center.x * 1000) / 1000, y: Math.round(state.center.y * 1000) / 1000 } : null });
trace.opening = snap();
const fireEl = (ev, e) => (el.listeners[ev] || []).forEach((f) => f(e));
const press = (name) => {
  controlNodes.forEach((c) => walk(c.node, (n) => { if (n.getAttribute('data-rt') === name && !n.hidden) (n.listeners.click || []).forEach((f) => f({ preventDefault() {}, stopPropagation() {} })); }));
};

for (const s of i.steps || []) {
  if (s.do === 'resize') { cur.w = s.w; cur.h = s.h; if (resizeCb) resizeCb(); }
  else if (s.do === 'touch') fireEl('touchstart', {});
  else if (s.do === 'wheel') fireEl('wheel', {});
  else if (s.do === 'press') fireEl('mousedown', {});
  else if (s.do === 'drag') fire('dragstart');
  else if (s.do === 'zoom') fire('zoomstart');
  else if (s.do === 'finger') { const t = new Array(s.n).fill({}); fireEl('touchstart', { touches: t }); fireEl('touchmove', { touches: t, preventDefault() {} }); }
  else if (s.do === 'wait') advance(s.ms);
  else if (s.do === 'pan') { state.center = { x: state.center.x + s.dx, y: state.center.y + s.dy }; fire('dragstart'); }
  else if (s.do === 'tapCluster') {
    const c = gatherNow().find((x) => x.count > 1);
    if (c && c.group.opts.zoomToBoundsOnClick !== false) map.fitBounds(new Bounds(c.members.map((m) => [m.rec.lat, m.rec.lng])), { gather: true });
  }
  else if (s.do === 'control') press(s.name);
  else if (s.do === 'key') (docEl.listeners.keydown || []).forEach((f) => f({ key: s.key }));
}

const hint = (() => { let h = null; walk(el, (n) => { if (/\brt-map-hint\b/.test(n.className || '')) h = n; }); return h; })();
const buttons = [];
controlNodes.forEach((c) => walk(c.node, (n) => { const k = n.getAttribute && n.getAttribute('data-rt'); if (k) buttons.push({ name: k, label: n.getAttribute('aria-label') || '', text: n.textContent || '', hidden: !!n.hidden, position: c.position, pressed: n.getAttribute('aria-pressed') }); }));

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
  lines: lines.map((l) => ({ color: l.style.color, weight: l.style.weight, opacity: l.style.opacity, visible: visible(l) })),
  circles: circles.filter(visible).length,
  markers: markers.filter(visible).map((m) => ({ ...m.rec, inView: inView(m.rec.lat, m.rec.lng), clustered: gathers.some((g) => g.layers.indexOf(m) >= 0 && g.onMap) })),
  mapOpts,
  hint: hint ? { shown: hint.classList.contains('on'), text: hint.textContent, role: hint.getAttribute('role') } : null,
  buttons,
  full: { onMap: el.classList.contains('rt-map-full'), onBody: docEl.body.classList.contains('rt-map-lock') },
  invalidated,
  opening: trace.opening, view: snap(),
  gatherOpts: gathers.map((g) => g.opts && { maxClusterRadius: g.opts.maxClusterRadius, zoomToBoundsOnClick: g.opts.zoomToBoundsOnClick, spiderfyOnMaxZoom: g.opts.spiderfyOnMaxZoom, showCoverageOnHover: g.opts.showCoverageOnHover }),
  // What a visitor sees as counts: each group of overlapping pins, with the badge the page asked for.
  gathered: gatherNow().map((c) => ({ count: c.count, badge: c.group.opts.iconCreateFunction ? (c.group.opts.iconCreateFunction({ getChildCount: () => c.count, getAllChildMarkers: () => c.members }) || {}).html || '' : '' })),
}));
