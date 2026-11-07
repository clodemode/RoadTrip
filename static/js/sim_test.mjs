import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const sim = createRequire(import.meta.url)('./sim.js');

test('haversine: one degree of latitude is about 69.09 miles', () => {
  assert.ok(Math.abs(sim.haversine(30, -97, 31, -97) - 69.09) < 0.1);
});
test('haversine: zero distance and symmetry', () => {
  assert.equal(sim.haversine(30.2672, -97.7431, 30.2672, -97.7431), 0);
  var a = sim.haversine(30.2672, -97.7431, 29.4241, -98.4936), b = sim.haversine(29.4241, -98.4936, 30.2672, -97.7431);
  assert.ok(Math.abs(a - b) < 1e-9);
  assert.ok(Math.abs(a - 74) < 3); // Austin to San Antonio, straight line
});
test('bearing: due north, east, south', () => {
  assert.ok(Math.abs(sim.bearing(30, -97, 31, -97) - 0) < 1e-6);
  assert.ok(Math.abs(sim.bearing(0, 0, 0, 1) - 90) < 1e-6);
  assert.ok(Math.abs(sim.bearing(31, -97, 30, -97) - 180) < 1e-6);
});
test('pointAt: walking d miles from a point lands d miles away, on the line', () => {
  var from = [30.2672, -97.7431], to = [29.4241, -98.4936];
  [1, 10, 50].forEach(function (d) {
    var p = sim.pointAt(from[0], from[1], to[0], to[1], d);
    assert.ok(Math.abs(sim.haversine(from[0], from[1], p[0], p[1]) - d) < 1e-6, 'd=' + d);
    var rest = sim.haversine(p[0], p[1], to[0], to[1]);
    assert.ok(Math.abs(d + rest - sim.haversine(from[0], from[1], to[0], to[1])) < 1e-4);
  });
});
test('pointAt: never walks past the destination', () => {
  var p = sim.pointAt(30, -97, 30.01, -97, 500);
  assert.ok(Math.abs(p[0] - 30.01) < 1e-6 && Math.abs(p[1] + 97) < 1e-6);
});
test('defaults: time compression by rung', () => {
  assert.equal(sim.defaultX(1), 1);
  assert.equal(sim.defaultX(10), 10);
  assert.equal(sim.defaultX(100), 60);
});
test('parse: reads sim params, rejects a bad pair', () => {
  var o = sim.parse('?sim=10&from=30.2672,-97.7431&to=30.389,-97.948');
  assert.equal(o.miles, 10); assert.equal(o.x, 10); assert.equal(o.mph, 60);
  assert.deepEqual(o.from, [30.2672, -97.7431]);
  assert.equal(sim.parse('?sim=10&to=nope').to, null);
  assert.equal(sim.parse('?foo=1'), null);
});
test('stopUrl drops the sim params and keeps the rest', () => {
  assert.equal(sim.stopUrl('/driver', '?sim=1&from=1,2&to=3,4&x=5&mph=6&a=b'), '/driver?a=b');
  assert.equal(sim.stopUrl('/driver', '?sim=1&to=3,4'), '/driver');
});
test('walk: speed is mph times compression, one real second at a time', () => {
  var o = { from: [30, -97], to: [31, -97], miles: 10, x: 10, mph: 60 };
  var w = sim.walk(o, 6); // 6 s * 60 mph * 10 / 3600 = 1 mile
  assert.ok(Math.abs(w.d - 1) < 1e-9);
  assert.equal(w.done, false);
  assert.ok(Math.abs(sim.haversine(30, -97, w.pos[0], w.pos[1]) - 1) < 1e-6);
});
test('walk: stops at the rung distance and stays there', () => {
  var o = { from: [30, -97], to: [31, -97], miles: 1, x: 60, mph: 60 };
  var w = sim.walk(o, 3600);
  assert.equal(w.done, true);
  assert.ok(Math.abs(w.d - 1) < 1e-9);
  assert.ok(Math.abs(sim.haversine(30, -97, w.pos[0], w.pos[1]) - 1) < 1e-6);
});
test('walk: a destination nearer than the rung ends the drive at the destination', () => {
  var o = { from: [30, -97], to: [30.01, -97], miles: 100, x: 60, mph: 60 };
  var w = sim.walk(o, 3600);
  assert.equal(w.done, true);
  assert.ok(Math.abs(w.pos[0] - 30.01) < 1e-6);
});
test('throttle: at most one HUD fetch per five seconds', () => {
  assert.equal(sim.mayFetch(0, 10000), true);
  assert.equal(sim.mayFetch(1000, 5999), false);
  assert.equal(sim.mayFetch(1000, 6000), true);
});
