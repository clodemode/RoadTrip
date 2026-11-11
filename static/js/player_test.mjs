import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// player.js with no #rt-player on the page: only its pure parts are exported.
function load(file) {
  const ctx = { module: { exports: {} }, document: { getElementById: () => null }, encodeURIComponent, Math, JSON, Object };
  vm.runInNewContext(readFileSync(new URL(file, import.meta.url), 'utf8'), ctx);
  return ctx.module.exports;
}
const P = load('./player.js');

test('nextPlayable skips tracks with no address and says -1 at the end', () => {
  const t = [{ src: 'a' }, { src: null }, { src: 'c' }, { src: null }];
  assert.equal(P.nextPlayable(t, -1), 0);
  assert.equal(P.nextPlayable(t, 0), 2);
  assert.equal(P.nextPlayable(t, 2), -1);
  assert.equal(P.nextPlayable([], -1), -1);
});

test('metadataFor carries title, artist, album and the art', () => {
  const m = P.metadataFor({ title: 'One', artist: 'A' }, { album: 'Rec', albumartist: 'B', art: 'https://x.example.test/art' });
  assert.deepEqual(JSON.parse(JSON.stringify(m)), { title: 'One', artist: 'A', album: 'Rec', artwork: [{ src: 'https://x.example.test/art' }] });
  assert.equal(P.metadataFor({ title: 'T' }, { album: 'Rec', albumartist: 'B' }).artist, 'B');
});

test('stateBody carries numbers and names and never an address', () => {
  const b = P.stateBody({ album_id: 42, album: 'Rec', albumartist: 'B', art: 'https://x.example.test/a', tracks: [{ src: 'https://y.example.test/s?sig=1' }] }, 2, 61.54);
  assert.equal(b, 'album_id=42&album=Rec&artist=B&track_idx=2&position_s=61.5');
  assert.ok(!/https?:|sig=|x\.example/.test(b));
});

test('the tap unlocks the element before any fetch', async () => {
  const log = [];
  const audio = { set src(v) { log.push('src:' + (v === P.SILENT ? 'silent' : v)); }, play() { log.push('play'); return Promise.resolve(); } };
  const p = P.unlockThenLoad(audio, (id) => { log.push('fetch:' + id); return Promise.resolve({ tracks: [] }); }, 42);
  assert.deepEqual(log, ['src:silent', 'play', 'fetch:42'], 'play() is called synchronously inside the tap, before the fetch');
  await p;
});

test('a play() that throws or rejects never breaks the unlock', async () => {
  const audio = { src: '', play() { return Promise.reject(new Error('x')); } };
  await P.unlockThenLoad(audio, () => Promise.resolve(1), 1);
  const audio2 = { src: '', play() { throw new Error('x'); } };
  await P.unlockThenLoad(audio2, () => Promise.resolve(1), 1);
});

test('hud telemetry never carries an address', () => {
  const H = load('./hud.js');
  assert.equal(H.telemetryBody('player', { src: 'https://x.example.test/a?sig=1' }), null);
  assert.equal(H.telemetryBody('player', { nested: { u: '//x.example.test/a' } }), null);
  assert.ok(H.telemetryBody('nav_tap', { app: 'waze', mode: 'trip' }));
});

test('toggleCard opens and closes the card body and says so', async () => {
  const { createRequire } = await import('node:module');
  const { toggleCard } = createRequire(import.meta.url)('./player.js');
  const attrs = {}; const btn = { setAttribute: (k, v) => { attrs[k] = v; } };
  const body = { hidden: true };
  assert.equal(toggleCard(btn, body), true);
  assert.equal(body.hidden, false);
  assert.equal(attrs['aria-expanded'], 'true');
  assert.equal(toggleCard(btn, body), false);
  assert.equal(body.hidden, true);
  assert.equal(attrs['aria-expanded'], 'false');
});

// The watchdog with a hand-driven clock: a tap always ends in sound or a sentence.
function clock() {
  const q = []; let t = 0, id = 0;
  return { setTimeout: (f, ms) => { q.push({ id: ++id, at: t + ms, f }); return id; }, clearTimeout: (i) => { const k = q.findIndex((x) => x.id === i); if (k >= 0) q.splice(k, 1); },
    run(to) { for (;;) { q.sort((a, b) => a.at - b.at); if (!q.length || q[0].at > to) break; const x = q.shift(); t = x.at; x.f(); } t = to; } };
}
test('the watchdog says it is still trying at 8 s, tries once more at 20 s, gives up 15 s after that', () => {
  const c = clock(), log = [];
  const w = P.createWatch({ setTimeout: c.setTimeout, clearTimeout: c.clearTimeout, ms: { stall: 8000, retry: 20000, giveUp: 15000 },
    onStall: (p) => log.push('stall:' + p), onRetry: (p) => log.push('retry:' + p), onGiveUp: (p) => log.push('giveup:' + p) });
  w.arm('stream');
  c.run(7999); assert.deepEqual(log, []);
  c.run(8000); assert.deepEqual(log, ['stall:stream']);
  c.run(20000); assert.deepEqual(log, ['stall:stream', 'retry:stream']);
  c.run(35000); assert.deepEqual(log, ['stall:stream', 'retry:stream', 'stall:stream', 'giveup:stream']);
  assert.equal(w.armed(), false);
});
test('the first sound stands the watchdog down, and a later start gets its own retry', () => {
  const c = clock(), log = [];
  const w = P.createWatch({ setTimeout: c.setTimeout, clearTimeout: c.clearTimeout, ms: { stall: 8, retry: 20, giveUp: 15 },
    onStall: () => log.push('stall'), onRetry: () => log.push('retry'), onGiveUp: () => log.push('giveup') });
  w.arm('stream'); c.run(5); w.ok(); c.run(100);
  assert.deepEqual(log, []);
  w.arm('stream'); c.run(200);
  assert.deepEqual(log, ['stall', 'retry', 'stall', 'giveup']);
});
test('a failure report carries an event word and numbers only, never a place, a title or an address', () => {
  assert.equal(P.reportBody('first_sound', { ms: 1234.4, album_id: 42, track_idx: 2, lat: 30.1, title: 'Song', src: 'https://x.example.test/a' }),
    'event=first_sound&ms=1234&album_id=42&track_idx=2');
  assert.equal(P.reportBody('play_timeout', { code: 'Stream Phase!' }), 'event=play_timeout&code=stream_phase_');
  assert.equal(P.reportBody('stream_error', { code: 'media_' + 'x'.repeat(40) }).length, 'event=stream_error&code='.length + 24);
  assert.equal(P.reportBody('Bad Event', {}), '');
  assert.equal(P.reportBody('ask_timeout', { ms: 'soon' }), 'event=ask_timeout');
});
