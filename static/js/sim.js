/* roadtrip — simulated GPS drive for the driver screen (the Trailer Test).
 *
 * Inert unless the URL has ?sim=1, 10 or 100 (the rung, in miles). Then it
 * replaces the browser geolocation with a fake that walks the great circle
 * from "from" toward "to" at mph times the time compression, one fix per real
 * second, and stops at the rung distance. Nothing is stored anywhere; the
 * fixes live only in this page. Loaded before hud.js so hud.js sees the fake.
 * Params: sim=1|10|100, from=lat,lng, to=lat,lng, x=compression, mph=60.
 */
(function (root) {
  'use strict';
  var R_MI = 3958.7613, RAD = Math.PI / 180;
  var SIM_KEYS = ['sim', 'from', 'to', 'x', 'mph'];

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
    var miles = parseInt(q.get('sim'), 10);
    if ([1, 10, 100].indexOf(miles) < 0) return null;
    return { miles: miles, from: pair(q.get('from')), to: pair(q.get('to')),
      x: num(q.get('x'), defaultX(miles)), mph: num(q.get('mph'), 60) };
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
  function mayFetch(last, now) { return now - last >= 5000; }

  var api = { haversine: haversine, bearing: bearing, pointAt: pointAt, defaultX: defaultX,
    parse: parse, stopUrl: stopUrl, walk: walk, mayFetch: mayFetch };
  if (typeof module !== 'undefined' && module.exports) { module.exports = api; return; }

  // ---- browser side: only when the URL asks for it ------------------------------
  var o = parse(root.location.search);
  if (!o) return;
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
  var realFetch = root.fetch ? root.fetch.bind(root) : null, lastHud = 0, lastBody = null;
  if (realFetch) {
    root.fetch = function (url, init) {
      if (String(url).indexOf('/driver/hud.json') !== 0) return realFetch(url, init);
      var now = Date.now();
      if (lastBody !== null && !mayFetch(lastHud, now)) {
        return Promise.resolve(new Response(lastBody, { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      lastHud = now;
      return realFetch(url, init).then(function (r) {
        return r.clone().text().then(function (t) { if (r.ok) lastBody = t; return r; });
      });
    };
  }

  function mkFix(pos, heading, moving) {
    return { coords: { latitude: pos[0], longitude: pos[1], accuracy: 10, altitude: null,
      altitudeAccuracy: null, heading: heading, speed: moving ? o.mph * 0.44704 * o.x : 0 }, timestamp: Date.now() };
  }
  function emit(pos, heading, moving) {
    var fix = mkFix(pos, heading, moving);
    Object.keys(watchers).forEach(function (k) { watchers[k].ok(fix); });
  }
  function tick() {
    var w = walk(o, (Date.now() - t0) / 1000);
    emit(w.pos, bearing(o.from[0], o.from[1], o.to[0], o.to[1]), !w.done);
    if (w.done) {
      clearInterval(timer); timer = null;
      say('arrived, ' + o.miles + ' mi driven at ' + o.x + 'x');
    } else {
      say(w.d.toFixed(1) + ' of ' + o.miles + ' mi at ' + o.mph + ' mph x' + o.x);
    }
  }
  function begin() {
    if (started) return;
    started = true;
    t0 = Date.now();
    say('0.0 of ' + o.miles + ' mi at ' + o.mph + ' mph x' + o.x);
    tick();
    if (timer === null && !(walk(o, 0).done)) timer = setInterval(tick, 1000);
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
    haveFrom().then(haveTo).then(begin).catch(function () {
      say('no start point: add from=lat,lng');
      Object.keys(watchers).forEach(function (k) { if (watchers[k].err) watchers[k].err({ code: 2, message: 'no start point' }); });
    });
  }
  var fake = {
    watchPosition: function (ok, err) { var id = nextId++; watchers[id] = { ok: ok, err: err }; go(); return id; },
    getCurrentPosition: function (ok, err) {
      var id = nextId++;
      if (started) { var w = walk(o, (Date.now() - t0) / 1000); ok(mkFix(w.pos, bearing(o.from[0], o.from[1], o.to[0], o.to[1]), !w.done)); return; }
      watchers[id] = { ok: function (f) { delete watchers[id]; ok(f); }, err: err };
      go();
    },
    clearWatch: function (id) { delete watchers[id]; }
  };
  try { Object.defineProperty(nav, 'geolocation', { value: fake, configurable: true }); } catch (e) { say('could not take over GPS'); }
})(typeof window !== 'undefined' ? window : globalThis);
