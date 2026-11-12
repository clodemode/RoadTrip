import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// listen.js: the pure parts load with no window and no document.
function load(file) {
  const ctx = { module: { exports: {} }, Date, isNaN, Math };
  vm.runInNewContext(readFileSync(new URL(file, import.meta.url), 'utf8'), ctx);
  return ctx.module.exports;
}
const L = load('./listen.js');
const N = load('./nowplaying.js');
// Newest first, as /tracks.json lists them.
const tracks = [{ id: 'c3' }, { id: 'b2' }, { id: 'a1' }];

test('a track that ends goes on to the next NEWER row', () => {
  assert.equal(L.nextId(tracks, 'a1'), 'b2');
  assert.equal(L.nextId(tracks, 'b2'), 'c3');
});

test('the newest row, an unknown row and an empty list have no next', () => {
  assert.equal(L.nextId(tracks, 'c3'), null);
  assert.equal(L.nextId(tracks, 'zz'), null);
  assert.equal(L.nextId([], 'a1'), null);
  assert.equal(L.nextId(null, 'a1'), null);
});

test('the chip plays what is current, else the newest', () => {
  assert.equal(L.currentId({ now: { id: 'n1' }, last: null }), 'n1');
  assert.equal(L.currentId({ now: null, last: { id: 'l1' } }), 'l1');
  assert.equal(L.currentId({ now: null, last: null }), null);
  assert.equal(L.currentId({ now: { track: 'x' } }), null);
});

test('each failure has one plain sentence', () => {
  assert.match(L.errorText(404), /not available/);
  assert.match(L.errorText(429), /Try again in a minute/);
  assert.match(L.errorText(502), /library is not answering/);
  assert.match(L.errorText(503), /library is not answering/);
  assert.match(L.errorText(0), /would not play/);
  assert.match(L.errorText(500), /would not play/);
  for (const s of [404, 429, 502, 0]) assert.ok(L.errorText(s).endsWith('.'));
});

test('a Play button is drawn only when listening is on and the row has a name', () => {
  const esc = (s) => String(s);
  const p = { id: 'a1', track: 'T', album: 'A', artist: 'R', started_at: '2026-11-12T14:05:00Z' };
  assert.match(N.rowHtml(p, esc, true), /<button type="button" class="np-play" data-id="a1"/);
  assert.doesNotMatch(N.rowHtml(p, esc, false), /np-play/);
  assert.doesNotMatch(N.rowHtml(p, esc), /np-play/);
  assert.doesNotMatch(N.rowHtml({ track: 'T', album: 'A', artist: 'R' }, esc, true), /np-play/);
});
