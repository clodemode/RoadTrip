// Runs player.js (the shell's player, with its nest) against a fake page and a
// scripted network, and prints what the audio element and the network saw.
// Read by continuous_node_test.go; handy by hand:
//   echo '{"tracks":4,"actions":[{"do":"open","album":42}]}' | node static/js/continuous_harness.mjs
// Input: {tracks, actions, fetchMode: ok|cors|expired|hang, ceiling, bytes}.
// Actions: open, frameTap, frameLoad{path,search,hash}, ended{times}, pause, signOut, settle.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const input = JSON.parse(readFileSync(0, 'utf8'));
const mode = input.fetchMode || 'ok', bytes = input.bytes || 1000000, N = input.tracks || 4;
const out = { reports: [], audioLog: [], events: [], trackFetches: [], albumFetches: 0, statusHistory: [], created: [], revoked: [], logs: [],
  history: [], statePosts: 0, frameLoads: [], maxInflight: 0, held: [], title: '', syncEvents: [] };
const idOf = (src) => Number((/\/[tb](\d+)\?/.exec(src) || [])[1]);

function el(id) {
  const e = { id, hidden: false, attrs: {}, listeners: {}, disabled: false, style: {}, _t: '',
    setAttribute(k, v) { this.attrs[k] = String(v); }, getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
    removeAttribute(k) { delete this.attrs[k]; },
    addEventListener(t, f) { (this.listeners[t] = this.listeners[t] || []).push(f); },
    fire(t, x) { for (const f of this.listeners[t] || []) f(Object.assign({ type: t, target: this }, x)); } };
  Object.defineProperty(e, 'textContent', { get() { return this._t; }, set(v) { this._t = v; if (id === 'rt-status') out.statusHistory.push(v); } });
  return e;
}
const els = {};
if (input.resume) { els['rt-resume'] = el('rt-resume'); for (const [k, v] of Object.entries(input.resume)) els['rt-resume'].attrs['data-' + k] = String(v); }
for (const id of ['rt-player', 'rt-art', 'rt-title', 'rt-artist', 'rt-track', 'rt-play', 'rt-next', 'rt-status', 'rt-nest', 'rt-frame', 'hud-drivetracks']) els[id] = el(id);
const audio = el('rt-audio');
els['rt-audio'] = audio;
audio.paused = true; audio.currentTime = 0; audio._src = '';
Object.defineProperty(audio, 'src', { get() { return this._src; }, set(v) { this._src = v; const l = 'src:' + (v.startsWith('data:') ? 'silent' : v); out.audioLog.push(l); out.events.push(l); } });
// audioMode 'silent': the stream never makes a sound (no 'playing'), as on a dead link.
audio.play = function () { this.paused = false; out.audioLog.push('play'); out.events.push('play');
  if (input.audioMode !== 'silent' || this._src.startsWith('data:')) queueMicrotask(() => { if (!this._src.startsWith('data:')) out.events.push('sound'); this.fire('playing'); });
  return Promise.resolve(); };
audio.pause = function () { this.paused = true; out.audioLog.push('pause'); this.fire('pause'); };
audio.load = function () { out.audioLog.push('load'); };
const frameWin = { location: { pathname: '/', search: '', hash: '' }, document: { title: '' } };
els['rt-frame'].contentWindow = frameWin;

let inflight = 0, resigned = false, last = { idx: -1, album: '' };
const doc = (id) => ({ album_id: id, album: id === 42 ? 'First' : 'Second', albumartist: 'Artist', art: '',
  tracks: Array.from({ length: N }, (_, i) => ({ title: 'Song ' + (i + 1), src: `https://r2.example.test/${id === 42 ? 't' : 'b'}${i}?sig=${resigned ? 2 : 1}` })) });
const tick = () => new Promise((r) => setImmediate(r));
async function fetchImpl(url, opts) {
  opts = opts || {};
  if (url === '/driver/roadtracks/event') {
    out.reports.push(Object.assign({ _keepalive: opts.keepalive === true, _type: (opts.headers || {})['Content-Type'] }, Object.fromEntries(new URLSearchParams(opts.body))));
    return { ok: false, status: 404 };
  }
  if (/\/driver\/roadtracks\/album\/(\d+)/.test(url)) {
    out.events.push('fetch:' + url);
    out.albumFetches++;
    if (input.albumMode === 'hang') return new Promise(() => {});
    if (out.albumFetches > 1 && mode !== 'ok') resigned = true;
    const id = Number(/album\/(\d+)/.exec(url)[1]);
    return { ok: true, status: 200, json: async () => doc(id) };
  }
  if (url === '/driver/roadtracks/state') { out.statePosts++; return { ok: true, status: 200 }; }
  // a track download
  out.trackFetches.push(idOf(url)); out.events.push('dl:' + idOf(url)); last = { idx: idOf(url), album: url.includes('/t') ? 't' : 'b' };
  inflight++; out.maxInflight = Math.max(out.maxInflight, inflight);
  try {
    await tick();
    if (mode === 'hang') return new Promise(() => {});
    if (mode === 'cors') throw new TypeError('Failed to fetch');
    if (mode === 'expired' && url.endsWith('sig=1')) return { ok: false, status: 403, headers: { get: () => '' }, blob: async () => new Blob([]) };
    return { ok: true, status: 200, headers: { get: (h) => (h.toLowerCase() === 'content-type' ? 'audio/ogg; codecs=opus' : '') }, blob: async () => new Blob([new Uint8Array(input.bytes || 1000)]) };
  } finally { inflight--; }
}
let n = 0;
const ctx = { console: { log: (...a) => out.logs.push(a.join(' ')), warn: (...a) => out.logs.push(a.join(' ')) },
  document: { getElementById: (id) => els[id] || null, addEventListener() {}, querySelectorAll: () => [], title: '' },
  navigator: {}, history: { replaceState: (a, b, u) => out.history.push(u) },
  location: { pathname: '/plan', search: '', hash: '' },
  fetch: fetchImpl, setTimeout, clearTimeout, setInterval: () => 1, Blob, Promise, JSON, Math, Object, Array, Number, String, encodeURIComponent, queueMicrotask, Uint8Array,
  URL: { createObjectURL: (b) => { const u = 'blob:test/' + (++n); out.created.push({ url: u, type: b.type, size: b.size, idx: last.idx, album: last.album }); return u; },
    revokeObjectURL: (u) => { const st = ctx.window.RTPlayer._state(); const c = out.created.findIndex((x) => x.url === u); out.revoked.push({ url: u, created: c, idx: out.created[c].idx, cur: st.cur, held: st.held }); } } };
ctx.window = ctx; ctx.addEventListener = () => {};
ctx.RTDriveTracks = () => Promise.resolve();
vm.createContext(ctx);
vm.runInContext(readFileSync(new URL('./player.js', import.meta.url), 'utf8'), ctx);
const P = ctx.RTPlayer;
// which song each created blob holds: the download that made it
out.createdSong = [];
const settle = async () => { for (let i = 0; i < 40; i++) await tick(); };
const startArgs = (id) => ({ album_id: id, album: id === 42 ? 'First' : 'Second', albumartist: 'Artist' });
if (input.ceiling) P.configNest({ ceiling: input.ceiling });
if (input.times) P.configTimes(input.times);
for (const a of input.actions || []) {
  if (a.do === 'open') { P.start(startArgs(a.album || 42)); await settle(); }
  else if (a.do === 'frameTap') { out.events.length = 0; P.start(startArgs(a.album || 42)); out.syncEvents = out.events.slice(); await settle(); }
  else if (a.do === 'frameLoad') {
    const before = out.audioLog.length;
    frameWin.location = { pathname: a.path, search: a.search || '', hash: a.hash || '' };
    frameWin.document.title = 'Title of ' + a.path;
    els['rt-frame'].fire('load');
    await settle();
    out.frameLoads.push({ before, after: out.audioLog.length });
  }
  else if (a.do === 'ended') { for (let i = 0; i < (a.times || 1); i++) { audio.fire('ended'); await settle(); } }
  else if (a.do === 'pause') { out.statePosts = 0; els['rt-play'].fire('click'); await settle(); }
  else if (a.do === 'signOut') { out.statePosts = 0; P.stop(); await settle(); }
  else if (a.do === 'settle') await settle();
  else if (a.do === 'wait') { await new Promise((r) => setTimeout(r, a.ms)); await settle(); }
  else if (a.do === 'play') { out.events.length = 0; els['rt-play'].fire('click'); out.syncEvents = out.events.slice(); await settle(); }
  else if (a.do === 'audio') { audio.fire(a.event); await settle(); }
}
await settle();
const st = P._state();
out.held = st.held; out.cur = st.cur;
out.paused = audio.paused; out.src = audio._src; out.status = els['rt-status']._t; out.trackText = els['rt-track']._t;
out.nest = els['rt-nest']._t; out.playText = els['rt-play']._t; out.resumeHidden = els['rt-resume'] ? els['rt-resume'].hidden : null; out.title = ctx.document.title; out.hasRetry = true;
console.log(JSON.stringify(out));
process.exit(0);
