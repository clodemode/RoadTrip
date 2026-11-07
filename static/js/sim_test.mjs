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
  assert.equal(sim.mayFetch(0, 10000, true, true), true);
  assert.equal(sim.mayFetch(1000, 5999, true, true), false);
  assert.equal(sim.mayFetch(1000, 6000, true, true), true);
});
test('mayFetch: the first request that carries a position always goes through', () => {
  // hud.js asks once on load (no position) and again on the first fix; the
  // throttle must not answer the first-fix ask with the no-position reply.
  assert.equal(sim.mayFetch(1000, 2000, false, true), true);
  assert.equal(sim.mayFetch(1000, 2000, true, true), false);
  assert.equal(sim.mayFetch(1000, 2000, false, false), false);
});

// ---- corridor=kgn-yyz: a randomized, seeded drive along Highway 401 ----------------
test('corridorPoints: kgn-yyz runs Kingston to Toronto, unknown names are null', () => {
  var p = sim.corridorPoints('kgn-yyz');
  assert.equal(p.length, 18);
  assert.deepEqual(p[0], [44.2290, -76.4810]);
  assert.deepEqual(p[p.length - 1], [43.6532, -79.3832]);
  assert.equal(sim.corridorPoints('nope'), null);
});
test('parse: reads corridor and seed, and the rung all only with a corridor', () => {
  var o = sim.parse('?sim=100&corridor=kgn-yyz&seed=42');
  assert.equal(o.corridor, 'kgn-yyz'); assert.equal(o.seed, 42);
  assert.equal(sim.parse('?sim=10').corridor, null);
  assert.equal(sim.parse('?sim=10').seed, null);
  var all = sim.parse('?sim=all&corridor=kgn-yyz&seed=1');
  assert.ok(all.miles > 150 && all.miles < 180);
  assert.equal(sim.parse('?sim=all'), null);
});
test('walkPath: d=0 is the first point, d past the end is the last', () => {
  var pts = sim.corridorPoints('kgn-yyz');
  var a = sim.walkPath(pts, 0), z = sim.walkPath(pts, 99999);
  assert.deepEqual(a.pos, pts[0]);
  assert.deepEqual(z.pos, pts[pts.length - 1]);
  assert.ok(a.heading > 270 && a.heading < 360, 'Kingston to the 401 runs north-west: ' + a.heading);
});
test('corridorWalk: distance along is monotonic over 300 ticks', () => {
  var o = sim.parse('?sim=100&corridor=kgn-yyz&seed=7'), last = -1;
  for (var t = 0; t < 300; t++) {
    var w = sim.corridorWalk(o, t);
    assert.ok(w.d >= last, 'tick ' + t); last = w.d;
  }
  assert.ok(last > 0);
});
test('same seed, same drive; another seed, another drive', () => {
  var run = function (s) {
    var o = sim.parse('?sim=100&corridor=kgn-yyz&seed=' + s);
    return JSON.stringify([5, 77, 190].map(function (t) { return sim.corridorWalk(o, t); }));
  };
  assert.equal(run(11), run(11));
  assert.notEqual(run(11), run(12));
});
test('start offset is 0 to 5 miles along the corridor', () => {
  for (var s = 1; s <= 50; s++) {
    var d = sim.corridorWalk(sim.parse('?sim=100&corridor=kgn-yyz&seed=' + s), 0).start;
    assert.ok(d >= 0 && d <= 5, 'seed ' + s + ': ' + d);
  }
});
test('jitter: never more than 30 m from the un-jittered point; accuracy 5 to 25 m', () => {
  var pts = sim.corridorPoints('kgn-yyz');
  for (var k = 0; k < 300; k++) {
    var base = sim.walkPath(pts, k * 0.3);
    var j = sim.jitterFix(base.pos, base.heading, 99, k);
    var m = sim.haversine(base.pos[0], base.pos[1], j.pos[0], j.pos[1]) * 1609.344;
    assert.ok(m <= 30, 'k=' + k + ' ' + m);
    assert.ok(j.accuracy >= 5 && j.accuracy <= 25);
  }
});
test('speedAt: 55 to 75 mph, steady within a minute, capped by mph=', () => {
  for (var t = 0; t < 3600 * 3; t += 30) {
    var v = sim.speedAt(5, t);
    assert.ok(v >= 55 && v <= 75, 't=' + t + ' ' + v);
    assert.ok(sim.speedAt(5, t, 58) <= 58);
  }
  assert.equal(sim.speedAt(5, 0), sim.speedAt(5, 59));
  assert.ok(sim.prng(3)() === sim.prng(3)() && sim.prng(3)() !== sim.prng(4)());
});
test('rung caps the distance driven; all drives the whole corridor', () => {
  var o = sim.parse('?sim=1&corridor=kgn-yyz&seed=3&x=60');
  var w = sim.corridorWalk(o, 99999);
  assert.equal(w.done, true); assert.ok(Math.abs(w.d - 1) < 1e-9);
  var all = sim.corridorWalk(sim.parse('?sim=all&corridor=kgn-yyz&seed=3&x=60'), 999999);
  assert.equal(all.done, true);
  assert.deepEqual(all.pos, [43.6532, -79.3832]);
});
