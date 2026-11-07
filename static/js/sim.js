/* roadtrip — the simulated drive's pure helpers, and its one link in.
 *
 * The drive itself is held by the SERVER (sim.go): one position per signed-in
 * user, computed from the corridor, the seed, the start instant and the
 * compression when a request needs it. Nothing ticks in a tab, nothing is kept
 * in the browser, and this file no longer replaces navigator.geolocation or
 * intercepts any request. Every page and device signed in as the driver sees
 * the same car; a server-rendered banner says so and carries Stop.
 *
 * What is left: the pure helpers (the corridor, the seeded PRNG, speed and
 * jitter laws; node tests cover them and sim.go is a port of them), and one
 * browser behaviour. If the URL has sim= and/or corridor=, POST /driver/sim/start
 * once with x, seed, mph and the rung (sim=1|10|100 miles, sim=all = the whole
 * corridor) and replace the URL with the clean path. from= and to= are ignored:
 * the straight-line mode is retired; only the corridor drive remains.
 */
(function (root) {
  'use strict';
  var R_MI = 3958.7613, RAD = Math.PI / 180;
  var SIM_KEYS = ['sim', 'from', 'to', 'x', 'mph', 'corridor', 'seed'];
  var CORRIDORS = {
    'kgn-yyz': [[44.2290, -76.4810], [44.2615, -76.5600], [44.2800, -76.7200], [44.2600, -76.9500],
      [44.1900, -77.3800], [44.1150, -77.5800], [44.0500, -77.7400], [44.0000, -77.9000],
      [43.9700, -78.1700], [43.9550, -78.2950], [43.9200, -78.5900], [43.9150, -78.6900],
      [43.9000, -78.8600], [43.8900, -78.9400], [43.8600, -79.0300], [43.8450, -79.0900],
      [43.7900, -79.2500], [43.6532, -79.3832]]
  };
  var CORRIDOR_LABELS = { 'kgn-yyz': 'Kingston\u2192Toronto corridor' };

  function haversine(la1, lo1, la2, lo2) {
    var dLa = (la2 - la1) * RAD, dLo = (lo2 - lo1) * RAD;
    var a = Math.sin(dLa / 2) * Math.sin(dLa / 2) +
      Math.cos(la1 * RAD) * Math.cos(la2 * RAD) * Math.sin(dLo / 2) * Math.sin(dLo / 2);
    return 2 * R_MI * Math.asin(Math.min(1, Math.sqrt(a)));
  }
  function bearing(la1, lo1, la2, lo2) {
    var dLo = (lo2 - lo1) * RAD;
    var y = Math.sin(dLo) * Math.cos(la2 * RAD);
    var x = Math.cos(la1 * RAD) * Math.sin(la2 * RAD) - Math.sin(la1 * RAD) * Math.cos(la2 * RAD) * Math.cos(dLo);
    return (Math.atan2(y, x) / RAD + 360) % 360;
  }
  // pointAt walks d miles from (la1,lo1) toward (la2,lo2), never past it.
  function pointAt(la1, lo1, la2, lo2, d) {
    var total = haversine(la1, lo1, la2, lo2);
    if (total === 0 || d >= total) return [la2, lo2];
    var dr = d / R_MI, th = bearing(la1, lo1, la2, lo2) * RAD, p1 = la1 * RAD, l1 = lo1 * RAD;
    var p2 = Math.asin(Math.sin(p1) * Math.cos(dr) + Math.cos(p1) * Math.sin(dr) * Math.cos(th));
    var l2 = l1 + Math.atan2(Math.sin(th) * Math.sin(dr) * Math.cos(p1), Math.cos(dr) - Math.sin(p1) * Math.sin(p2));
    return [p2 / RAD, ((l2 / RAD + 540) % 360) - 180];
  }
  // ---- corridor: a seeded drive along a polyline ----------------------------------
  function corridorPoints(name) { return CORRIDORS[name] ? CORRIDORS[name].map(function (p) { return p.slice(); }) : null; }
  function pathLength(points) {
    var t = 0;
    for (var i = 1; i < points.length; i++) t += haversine(points[i - 1][0], points[i - 1][1], points[i][0], points[i][1]);
    return t;
  }
  // walkPath: the point d miles along the polyline and the current segment's bearing.
  function walkPath(points, d) {
    var left = Math.max(0, d), n = points.length;
    for (var i = 1; i < n; i++) {
      var a = points[i - 1], b = points[i], seg = haversine(a[0], a[1], b[0], b[1]);
      if (left <= seg || i === n - 1) {
        return { pos: left >= seg - 1e-9 ? [b[0], b[1]] : (left === 0 ? [a[0], a[1]] : pointAt(a[0], a[1], b[0], b[1], left)),
          heading: bearing(a[0], a[1], b[0], b[1]) };
      }
      left -= seg;
    }
    return { pos: points[0].slice(), heading: 0 };
  }
  // mulberry32: a small deterministic PRNG, [0,1).
  function prng(seed) {
    var a = seed >>> 0;
    return function () {
      a = (a + 0x6D2B79F5) >>> 0;
      var t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }
  function mix(seed, k) { return (seed ^ Math.imul(k + 1, 0x9E3779B1)) >>> 0; }
  // speedAt: mph at simulated second t. Re-drawn each 60 s from the seed (the PRNG
  // state is derived from seed and the minute), 55-75, capped by cap when given.
  function speedAt(seed, t, cap) {
    var v = 55 + 20 * prng(mix(seed, Math.floor(t / 60)))();
    return cap > 0 ? Math.min(v, cap) : v;
  }
  // milesAt: miles driven after simSec simulated seconds, summing the minute speeds.
  function milesAt(seed, simSec, cap) {
    var mi = 0, m = 0;
    for (; (m + 1) * 60 <= simSec; m++) mi += speedAt(seed, m * 60, cap) * 60 / 3600;
    return mi + speedAt(seed, m * 60, cap) * (simSec - m * 60) / 3600;
  }
  function startOffset(seed) { return 5 * prng(mix(seed, -1))(); }
  // jitterFix: up to 30 m sideways of the segment, accuracy 5-25 m; fix k of the run.
  function jitterFix(pos, heading, seed, k) {
    var r = prng(mix(seed, 100000 + k)), lat = (r() * 2 - 1) * 29.9, acc = 5 + 20 * r();
    var th = (heading + 90) * RAD, dn = lat * Math.cos(th), de = lat * Math.sin(th);
    return { pos: [pos[0] + dn / 111320, pos[1] + de / (111320 * Math.cos(pos[0] * RAD))], accuracy: acc };
  }
  function corridorWalk(o, elapsed) {
    var pts = corridorPoints(o.corridor), total = pathLength(pts), start = Math.min(startOffset(o.seed), total);
    var cap = Math.min(o.miles, total - start), simSec = elapsed * o.x;
    var d = Math.min(cap, milesAt(o.seed, simSec, o.mphGiven ? o.mph : 0)), done = d >= cap;
    var base = walkPath(pts, start + d), j = done ? { pos: base.pos, accuracy: 10 } : jitterFix(base.pos, base.heading, o.seed, Math.floor(elapsed));
    return { d: d, start: start, done: done, pos: j.pos, heading: base.heading, accuracy: j.accuracy,
      mph: speedAt(o.seed, simSec, o.mphGiven ? o.mph : 0) };
  }
  function defaultX(miles) { return miles >= 100 ? 60 : miles >= 10 ? 10 : 1; }

  function pair(s) {
    if (!s) return null;
    var m = /^\s*(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)\s*$/.exec(s);
    if (!m) return null;
    var a = parseFloat(m[1]), b = parseFloat(m[2]);
    return (Math.abs(a) <= 90 && Math.abs(b) <= 180) ? [a, b] : null;
  }
  function num(s, dflt) { var v = parseFloat(s); return v > 0 ? v : dflt; }
  function parse(search) {
    var q = new URLSearchParams(search || '');
    var corridor = CORRIDORS[q.get('corridor')] ? q.get('corridor') : null;
    var miles = q.get('sim') === 'all' && corridor ? pathLength(CORRIDORS[corridor]) : parseInt(q.get('sim'), 10);
    if (!corridor || q.get('sim') !== 'all') { if ([1, 10, 100].indexOf(miles) < 0) return null; }
    var seed = /^\d+$/.test(q.get('seed') || '') ? parseInt(q.get('seed'), 10) >>> 0 : null;
    return { miles: miles, from: pair(q.get('from')), to: pair(q.get('to')), corridor: corridor, seed: seed,
      x: num(q.get('x'), defaultX(miles)), mph: num(q.get('mph'), 60), mphGiven: num(q.get('mph'), 0) > 0 };
  }
  function stopUrl(path, search) {
    var q = new URLSearchParams(search || '');
    SIM_KEYS.forEach(function (k) { q.delete(k); });
    var s = q.toString();
    return path + (s ? '?' + s : '');
  }
  // walk: where the fake car is after elapsed real seconds.
  function walk(o, elapsed) {
    var total = haversine(o.from[0], o.from[1], o.to[0], o.to[1]);
    var cap = Math.min(o.miles, total);
    var d = Math.min(cap, o.mph * o.x * elapsed / 3600);
    return { d: d, pos: pointAt(o.from[0], o.from[1], o.to[0], o.to[1], d), done: d >= cap };
  }
  // mayFetch: whether a HUD ask goes to the server. The first ask that carries a
  // position always does: hud.js asks once on load with none, then again on the
  // first fix, and answering that from the no-position reply froze the HUD on
  // plan pace until hud.js next asked, a minute later.
  function mayFetch(last, now, lastHadPos, hasPos) {
    if (hasPos && !lastHadPos) return true;
    return now - last >= 5000;
  }

  var api = { haversine: haversine, bearing: bearing, pointAt: pointAt, defaultX: defaultX,
    parse: parse, stopUrl: stopUrl, walk: walk, mayFetch: mayFetch,
    corridorPoints: corridorPoints, walkPath: walkPath, prng: prng, speedAt: speedAt, jitterFix: jitterFix,
    corridorWalk: corridorWalk };
  if (typeof module !== 'undefined' && module.exports) { module.exports = api; return; }

  // ---- browser side: one POST when the URL asks for a drive ----------------------
  var loc = root.location;
  if (!loc || !root.fetch) return;
  var q = new URLSearchParams(loc.search || '');
  if (!q.has('sim') && !q.has('corridor')) return;
  var body = new URLSearchParams();
  body.set('corridor', CORRIDORS[q.get('corridor')] ? q.get('corridor') : 'kgn-yyz');
  ['seed', 'x', 'mph'].forEach(function (k) { if (q.get(k)) body.set(k, q.get(k)); });
  var rung = parseInt(q.get('sim'), 10);
  if (rung > 0) body.set('rung', String(rung));
  var clean = stopUrl(loc.pathname, loc.search);
  root.fetch('/driver/sim/start', { method: 'POST', credentials: 'same-origin', body: body })
    .then(function () { loc.replace(clean); }, function () { loc.replace(clean); });
})(typeof window !== 'undefined' ? window : globalThis);
