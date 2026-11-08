import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const { suggestMilestone } = createRequire(import.meta.url)('./post.js');

const choices = [
  { id: '7', lat: 44.2312, lng: -76.4860 },   // Kingston
  { id: '8', lat: 44.2400, lng: -76.4900 },   // about 1 km from 7
  { id: '9', lat: 43.6532, lng: -79.3832 },   // Toronto
];

test('picks the nearest milestone within 2 km', () => {
  assert.equal(suggestMilestone({ lat: 44.2395, lng: -76.4895 }, choices), '8');
  assert.equal(suggestMilestone({ lat: 44.2312, lng: -76.4861 }, choices), '7');
});

test('nothing within 2 km gives null', () => {
  assert.equal(suggestMilestone({ lat: 44.0, lng: -77.5 }, choices), null);
  assert.equal(suggestMilestone(null, choices), null);
  assert.equal(suggestMilestone({ lat: 44.2312, lng: -76.4860 }, []), null);
});

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

const { sectionsFor, locationReason } = createRequire(import.meta.url)('./post.js');

test('each post type opens the sections it needs', () => {
  assert.deepEqual(sectionsFor('photo'), { upload: true, textOpen: false });
  assert.deepEqual(sectionsFor('note'), { upload: false, textOpen: true });
  for (const k of ['sight', 'meal', 'lodging', 'mishap', 'tip']) {
    assert.deepEqual(sectionsFor(k), { upload: true, textOpen: true }, k);
  }
  assert.deepEqual(sectionsFor('nonsense'), { upload: true, textOpen: true });
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
