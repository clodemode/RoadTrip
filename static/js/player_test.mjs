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
