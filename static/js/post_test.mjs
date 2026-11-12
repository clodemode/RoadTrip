import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const { askLocation } = createRequire(import.meta.url)('./post.js');

test('the phone is asked for its location at once, and the fix is reported as 4 dp strings', () => {
  const calls = [];
  const geo = { getCurrentPosition: (ok, fail, opts) => { calls.push(opts); ok({ coords: { latitude: 44.16284, longitude: -77.38321, accuracy: 12.4 } }); } };
  let got = null;
  assert.equal(askLocation(geo, (f) => { got = f; }, () => assert.fail('no error expected')), true);
  assert.equal(calls.length, 1);
  assert.deepEqual(got, { lat: '44.1628', lng: '-77.3832', acc: 12 });
});

test('a refused or missing location reports an error and never throws', () => {
  let err = null;
  askLocation({ getCurrentPosition: (ok, fail) => fail({ code: 1 }) }, () => assert.fail('no fix expected'), (e) => { err = e; });
  assert.equal(err.code, 1);
  err = null;
  assert.equal(askLocation(undefined, () => assert.fail('no fix expected'), (e) => { err = e; }), false);
  assert.equal(err.code, 0);
});

const { carriesPhoto, noteBlocked, autoSubmit, changedInMore, sendDecision, FIX_WAIT_MS, locationReason } = createRequire(import.meta.url)('./post.js');

test('every post type but a note carries a photo', () => {
  assert.equal(carriesPhoto('note'), false);
  for (const k of ['photo', 'sight', 'meal', 'lodging', 'mishap', 'tip', 'nonsense']) {
    assert.equal(carriesPhoto(k), true, k);
  }
});

test('a words-only post with no words is blocked, and any word unblocks it', () => {
  assert.equal(noteBlocked('', ''), true);
  assert.equal(noteBlocked('  ', '\n'), true);
  assert.equal(noteBlocked(undefined, null), true);
  assert.equal(noteBlocked('Gas', ''), false);
  assert.equal(noteBlocked('', 'Pie was good.'), false);
});

test('a chosen photo is posted at once, unless More options is open and was changed', () => {
  assert.equal(autoSubmit(false, false), true);
  assert.equal(autoSubmit(true, false), true);   // opened and looked, changed nothing
  assert.equal(autoSubmit(false, true), true);   // changed, then closed: the page asks nothing more
  assert.equal(autoSubmit(true, true), false);
  // Fuzz This sits outside More options: ticking it is no change in it.
  const more = { contains: (n) => n === 'in-more' };
  assert.equal(changedInMore(more, 'f-fuzz', true), false);
  assert.equal(autoSubmit(true, changedInMore(more, 'f-fuzz', true)), true);
  assert.equal(changedInMore(more, 'in-more', true), true);
  assert.equal(changedInMore(more, 'in-more', false), false);  // the page's own defaults never count
});

test('a photo is sent once it is read and the phone has answered, held when the driver is posting by hand', () => {
  const ready = { auto: true, exifDone: true, fixNeeded: false, elapsed: 50 };
  assert.equal(sendDecision(ready), 'send');
  assert.equal(sendDecision({ ...ready, auto: false }), 'hold');
  assert.equal(sendDecision({ ...ready, exifDone: false }), 'wait');
  assert.equal(sendDecision({ ...ready, fixNeeded: true }), 'wait');
});

test('the wait for the phone is capped at about two seconds, then the post goes without it', () => {
  assert.equal(FIX_WAIT_MS, 2000);
  const slow = { auto: true, exifDone: false, fixNeeded: true };
  assert.equal(sendDecision({ ...slow, elapsed: FIX_WAIT_MS - 1 }), 'wait');
  assert.equal(sendDecision({ ...slow, elapsed: FIX_WAIT_MS }), 'send');
  assert.equal(sendDecision({ ...slow, elapsed: 60000 }), 'send');
  // A held photo stays held however long it waits.
  assert.equal(sendDecision({ ...slow, auto: false, elapsed: 60000 }), 'hold');
});

test('choosing a photo asks for a fresh, high-accuracy fix', () => {
  const seen = [];
  const geo = { getCurrentPosition: (ok, fail, opts) => { seen.push(opts); } };
  askLocation(geo, () => {}, () => {}, true);
  askLocation(geo, () => {}, () => {});
  assert.equal(seen[0].enableHighAccuracy, true);
  assert.equal(seen[0].maximumAge, 0);
  assert.equal(seen[1].enableHighAccuracy, false);
  assert.equal(seen[1].maximumAge, 60000);
});

test('a page that is not HTTPS says so instead of "denied"', () => {
  assert.equal(locationReason(false, 1), 'this page is not HTTPS, so the phone will not share it');
  assert.equal(locationReason(true, 1), 'denied');
  assert.equal(locationReason(true, 2), 'no fix');
  assert.equal(locationReason(undefined, 1), 'denied');
});

test('ladderChoice offers the best source and never picks No pin itself', () => {
  const { ladderChoice } = createRequire(import.meta.url)('./post.js');
  assert.equal(ladderChoice(true, true, true), 'exif');
  assert.equal(ladderChoice(false, true, true), 'browser');
  assert.equal(ladderChoice(false, false, true), 'checkpoint');
  assert.equal(ladderChoice(false, false, false), '');
});

test('uploadingLabel says what is happening', () => {
  const { uploadingLabel } = createRequire(import.meta.url)('./post.js');
  assert.equal(uploadingLabel(true, true), 'Starting the drive…');
  assert.equal(uploadingLabel(false, true), 'Posting…');
  assert.equal(uploadingLabel(false, false), 'Posting…');
});

test('paintThenSend waits two frames so the label is drawn, and sends once', () => {
  const { paintThenSend } = createRequire(import.meta.url)('./post.js');
  const frames = []; let timer = null; let sent = 0;
  paintThenSend((f) => frames.push(f), (f) => { timer = f; }, () => { sent++; });
  assert.equal(sent, 0, 'nothing is sent before the page has painted');
  frames.shift()();
  assert.equal(sent, 0, 'one frame is not yet a painted frame');
  frames.shift()();
  assert.equal(sent, 1);
  timer();
  assert.equal(sent, 1, 'the fallback timer never sends it twice');
});

test('paintThenSend still sends if the browser never gives a frame', () => {
  const { paintThenSend } = createRequire(import.meta.url)('./post.js');
  let timer = null; let sent = 0;
  paintThenSend(() => {}, (f) => { timer = f; }, () => { sent++; });
  timer();
  assert.equal(sent, 1);
});
