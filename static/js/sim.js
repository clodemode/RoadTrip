/* roadtrip — simulated GPS drive for the driver screen (the Trailer Test).
 *
 * Inert unless the URL has ?sim=1, 10 or 100 (the rung, in miles). Then it
 * replaces the browser geolocation with a fake that walks the great circle
 * from "from" toward "to" at mph times the time compression, one fix per real
 * second, and stops at the rung distance. Nothing is stored anywhere; the
 * fixes live only in this page. Loaded before hud.js so hud.js sees the fake.
 * Params: sim=1|10|100, from=lat,lng, to=lat,lng, x=compression, mph=60.
 *
 * corridor=kgn-yyz walks a polyline along Highway 401 from Kingston to Toronto
 * instead of the from->to great circle; from= and to= are IGNORED then (the
 * corridor decides), and the real position is never read. The drive is seeded:
 * seed=N (default a random 32-bit value, shown in the banner) repeats a run.
 * It starts 0-5 mi along the corridor, re-draws its speed every 60 simulated
 * seconds in 55-75 mph (mph= caps it when given), jitters each fix up to 30 m
 * sideways and reports 5-25 m accuracy. sim=all drives the whole corridor.
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

  // ---- browser side: only when the URL asks for it ------------------------------
  var o = parse(root.location.search);
  if (!o) return;
  if (o.corridor && o.seed === null) o.seed = Math.floor(Math.random() * 4294967296) >>> 0;
  var nav = root.navigator, realGeo = nav.geolocation;
  var watchers = {}, nextId = 1, timer = null, t0 = 0, started = false;
  var banner = document.createElement('div');
  banner.className = 'sim-banner';
  banner.setAttribute('role', 'status');
  var msg = document.createElement('span');
  var stop = document.createElement('a');
  stop.textContent = 'Stop';
  stop.setAttribute('href', stopUrl(root.location.pathname, root.location.search));
  banner.appendChild(msg);
  banner.appendChild(stop);
  // Fixed height, in flow at the top: it takes its space once, then never moves.
  banner.style.cssText = 'position:sticky;top:0;z-index:1000;box-sizing:border-box;height:2.25rem;' +
    'display:flex;align-items:center;justify-content:space-between;padding:0 .75rem;' +
    'background:#b91c1c;color:#fff;font:600 .85rem/1 system-ui,sans-serif;';
  stop.style.cssText = 'color:#fff;text-decoration:underline;';
  function mount() { document.body.insertBefore(banner, document.body.firstChild); }
  if (document.body) mount(); else document.addEventListener('DOMContentLoaded', mount);

  function say(t) { msg.textContent = 'SIMULATED GPS — ' + t; }
  say('waiting for a start point');

  // At most one HUD fetch per ~5 s, however often hud.js asks.
  var realFetch = root.fetch ? root.fetch.bind(root) : null, lastHud = 0, lastBody = null, lastHadPos = false;
  if (realFetch) {
    root.fetch = function (url, init) {
      if (String(url).indexOf('/driver/hud.json') !== 0) return realFetch(url, init);
      var now = Date.now(), hasPos = /[?&]lat=/.test(String(url));
      if (lastBody !== null && !mayFetch(lastHud, now, lastHadPos, hasPos)) {
        return Promise.resolve(new Response(lastBody, { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      lastHud = now;
      lastHadPos = hasPos;
      return realFetch(url, init).then(function (r) {
        return r.clone().text().then(function (t) { if (r.ok) lastBody = t; return r; });
      });
    };
  }

  function mkFix(pos, heading, moving, acc, mph) {
    return { coords: { latitude: pos[0], longitude: pos[1], accuracy: acc || 10, altitude: null,
      altitudeAccuracy: null, heading: heading, speed: moving ? mph * 0.44704 * o.x : 0 }, timestamp: Date.now() };
  }
  // step: the car now, for either mode, as one shape.
  function step(elapsed) {
    if (o.corridor) { var c = corridorWalk(o, elapsed); c.head = c.heading; return c; }
    var w = walk(o, elapsed);
    w.head = bearing(o.from[0], o.from[1], o.to[0], o.to[1]); w.accuracy = 10; w.mph = o.mph;
    return w;
  }
  function label() { return o.corridor ? CORRIDOR_LABELS[o.corridor] + ', seed ' + o.seed + ', ' : ''; }
  function emit(w) {
    var fix = mkFix(w.pos, w.head, !w.done, w.accuracy, w.mph);
    Object.keys(watchers).forEach(function (k) { watchers[k].ok(fix); });
  }
  function tick() {
    var w = step((Date.now() - t0) / 1000);
    emit(w);
    if (w.done) {
      clearInterval(timer); timer = null;
      say(label() + 'arrived, ' + w.d.toFixed(1) + ' mi driven at ' + o.x + 'x');
    } else {
      say(label() + w.d.toFixed(1) + ' of ' + Math.round(o.miles) + ' mi at ' + Math.round(w.mph) + ' mph x' + o.x);
    }
  }
  function begin() {
    if (started) return;
    started = true;
    t0 = Date.now();
    tick();
    if (timer === null && !step(0).done) timer = setInterval(tick, 1000);
  }
  // Start: the given "from", else the first real fix. Destination: the given
  // "to", else the HUD next stop, else straight north.
  function haveTo() {
    if (o.to) return Promise.resolve();
    return (realFetch ? realFetch('/driver/hud.json', { credentials: 'same-origin' }).then(function (r) { return r.json(); }) : Promise.reject())
      .then(function (h) { if (h && typeof h.next_lat === 'number') o.to = [h.next_lat, h.next_lng]; })
      .catch(function () {})
      .then(function () { if (!o.to) o.to = pointAt(o.from[0], o.from[1], o.from[0] + 5, o.from[1], o.miles + 1); });
  }
  function haveFrom() {
    if (o.from) return Promise.resolve();
    return new Promise(function (res, rej) {
      if (!realGeo) { rej(); return; }
      realGeo.getCurrentPosition(function (p) { o.from = [p.coords.latitude, p.coords.longitude]; res(); }, rej, { timeout: 15000 });
    });
  }
  function go() {
    if (o.corridor) { begin(); return; }
    haveFrom().then(haveTo).then(begin).catch(function () {
      say('no start point: add from=lat,lng');
      Object.keys(watchers).forEach(function (k) { if (watchers[k].err) watchers[k].err({ code: 2, message: 'no start point' }); });
    });
  }
  var fake = {
    watchPosition: function (ok, err) { var id = nextId++; watchers[id] = { ok: ok, err: err }; go(); return id; },
    getCurrentPosition: function (ok, err) {
      var id = nextId++;
      if (started) { var w = step((Date.now() - t0) / 1000); ok(mkFix(w.pos, w.head, !w.done, w.accuracy, w.mph)); return; }
      watchers[id] = { ok: function (f) { delete watchers[id]; ok(f); }, err: err };
      go();
    },
    clearWatch: function (id) { delete watchers[id]; }
  };
  try { Object.defineProperty(nav, 'geolocation', { value: fake, configurable: true }); } catch (e) { say('could not take over GPS'); }
})(typeof window !== 'undefined' ? window : globalThis);
