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
