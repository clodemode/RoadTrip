import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// nowplaying.js is pure: loaded with no window and no document.
function load(file) {
  const ctx = { module: { exports: {} }, Date, isNaN, Math };
  vm.runInNewContext(readFileSync(new URL(file, import.meta.url), 'utf8'), ctx);
  return ctx.module.exports;
}
const N = load('./nowplaying.js');
const play = { track: 'Come Together', artist: 'The Beatles', album: 'Abbey Road', year: 1969, started_at: '2026-11-12T14:05:00Z', note: 'Easy tempo.' };

test('a fresh play reads as Currently playing, with track, artist and album', () => {
  const t = N.chipText({ now: play, last: null, tracks: [play] });
  assert.equal(t.label, 'Currently playing');
  assert.equal(t.line, 'Come Together · The Beatles · Abbey Road');
  assert.equal(t.fresh, true);
});

test('an old play reads as Last played', () => {
  const t = N.chipText({ now: null, last: play, tracks: [play] });
  assert.equal(t.label, 'Last played');
  assert.equal(t.fresh, false);
});

test('a ride with no plays has no chip', () => {
  assert.equal(N.chipText({ now: null, last: null, tracks: [] }), null);
  assert.equal(N.chipText(null), null);
});

test('a hidden tab never polls; a visible one polls when a minute is up or it never has', () => {
  assert.equal(N.shouldPoll(true, 0, 1e9), false);
  assert.equal(N.shouldPoll(true, 1000, 1e9), false);
  assert.equal(N.shouldPoll(false, 0, 1e9), true);
  assert.equal(N.shouldPoll(false, 1e9, 1e9 + 10000), false);
  assert.equal(N.shouldPoll(false, 1e9, 1e9 + 60000), true);
});

test('a row shows the time with a zone, the album, the track and the note, escaped', () => {
  const esc = (s) => String(s).replace(/</g, '&lt;');
  const h = N.rowHtml({ ...play, track: 'A<b>' }, esc);
  assert.match(h, /A&lt;b>/);
  assert.match(h, /Abbey Road \(1969\)/);
  assert.match(h, /Easy tempo\./);
  assert.match(h, /np-when">[^<]+[A-Z]{2,5}|np-when">[^<]*GMT/);
  assert.equal(N.rowHtml({ ...play, note: '' }, esc).includes('np-note'), false);
  assert.equal(N.fmtWhen('not a time'), '');
});
