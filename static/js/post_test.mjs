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
