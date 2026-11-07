/* roadtrip — the driver screen's live parts.
 *
 * The page arrives drawn from the plan. This asks /driver/hud.json for the
 * rest: the leg and the miles from the phone's position when it is granted,
 * and the hazard chips. The position goes in that one request's query and is
 * kept nowhere. Everything shown is set as text; the one link, Waze, is the
 * address the server built. Telemetry is the phone's own and is only ever
 * what was measured: no figure is invented when a fix or a request fails.
 */
(function () {
  'use strict';
  // Telemetry to /driver/telemetry. telemetryBody is the one place a body is
  // built: it returns null for a coordinate-shaped key at any depth, so the
  // page can never send a position. Exported for node like sim.js.
  var BANNED = { lat: 1, lng: 1, latitude: 1, longitude: 1, coords: 1 };
  function hasBanned(v) {
    if (!v || typeof v !== 'object') return false;
    return Object.keys(v).some(function (k) { return BANNED[k.toLowerCase()] || hasBanned(v[k]); });
  }
  function telemetryBody(event, fields) {
    fields = fields || {};
    if (hasBanned(fields)) return null;
    return JSON.stringify({ event: event, fields: fields });
  }
  // Is the car moving? The phone's own speed above 2 m/s, or more than 200 m
  // covered since the oldest fix of the last 120 s. Pure; exported for node.
  function distM(a, b) {
    var R = 6371000, r = Math.PI / 180;
    var dLa = (b.lat - a.lat) * r, dLo = (b.lng - a.lng) * r;
    var h = Math.sin(dLa / 2) * Math.sin(dLa / 2) +
      Math.cos(a.lat * r) * Math.cos(b.lat * r) * Math.sin(dLo / 2) * Math.sin(dLo / 2);
    return 2 * R * Math.asin(Math.min(1, Math.sqrt(h)));
  }
  function isMoving(prev, cur) {
    if (!cur) return false;
    if (typeof cur.speed === 'number' && cur.speed > 2) return true;
    if (!prev || cur.t - prev.t > 120000 || cur.t <= prev.t) return false;
    return distM(prev, cur) > 200;
  }
  // The two nav telemetry bodies: the tap, sent at once, and its outcome.
  function navTapBody(app, mode) { return telemetryBody('nav_tap', { app: app, mode: mode }); }
  function navResultBody(app, mode, hidden) { return telemetryBody('nav_result', { app: app, mode: mode, hidden: !!hidden }); }
  if (typeof module !== 'undefined' && module.exports) module.exports = { telemetryBody: telemetryBody, isMoving: isMoving, navTapBody: navTapBody, navResultBody: navResultBody };
  function sendBody(b) {
    if (b === null) return;
    try { if (navigator.sendBeacon) navigator.sendBeacon('/driver/telemetry', b); } catch (e) { /* telemetry never breaks the page */ }
  }
  function send(event, fields) { sendBody(telemetryBody(event, fields)); }

  var root = document.getElementById('hud');
  if (!root) return;
  var $ = function (id) { return document.getElementById(id); };
  var fix = null, lastAsk = 0, busy = false, again = false, moving = false, hist = [];
  // A simulated drive is held by the server (sim.go): the page never asks the
  // phone for a position and never sends one. Every ask goes without lat/lng.
  var simOn = root.getAttribute('data-sim') === '1';

  function setText(id, v) { var el = $(id); if (el && v !== undefined && v !== null) el.textContent = v; }

  function paintChips(h) {
    var ul = $('hud-chips');
    ul.textContent = '';
    (h.hazards || []).forEach(function (c) {
      var li = document.createElement('li');
      li.className = 'hud-chip ' + c.state;
      li.textContent = c.text;
      ul.appendChild(li);
    });
  }

  // FreeLance: Destined. Everything is set as text; the one link is the Waze
  // address the server built.
  function paintFree(f) {
    setText('free-miles', f.miles);
    setText('free-eta', f.eta);
    var d = $('free-delay');
    d.textContent = f.delay || '';
    d.hidden = !f.delay;
    setText('free-msg', f.state === 'ok' ? '' : (f.text || ''));
    var w = $('free-waze');
    w.hidden = !f.waze;
    if (f.waze) w.setAttribute('href', f.waze);
    var alt = $('free-nav-alt');
    alt.hidden = !f.google;
    if (f.google) { $('free-google').setAttribute('href', f.google); $('free-apple').setAttribute('href', f.apple); }
    $('free-find').disabled = !f.can_picks;
  }

  // FreeLance: Aimless. The miles and a sentence, as text; picks on a fix.
  function paintAimless(f) {
    setText('free-miles', f.miles);
    setText('free-msg', f.text || '');
    $('free-find').disabled = !f.can_picks;
    paintClock();
  }

  // The phone's own clock and zone: Aimless has no route to take a zone from.
  function paintClock() {
    if (!$('aim-clock')) return;
    setText('aim-clock', clock());
    var z = '';
    try { z = new Date().toLocaleTimeString([], { timeZoneName: 'short' }).split(' ').pop(); } catch (e) { z = ''; }
    setText('aim-tz', z);
  }

  function paint(h) {
    if (h.free) {
      paintChips(h);
      if (h.free.state === 'aimless') paintAimless(h.free); else paintFree(h.free);
      return;
    }
    setText('hud-day', h.day_n ? h.day_n + ' of ' + h.day_total : '—');
    setText('hud-leg', h.leg_name);
    setText('hud-done', h.miles_done);
    setText('hud-left', h.miles_left);
    setText('hud-tonight', h.tonight_name);
    setText('hud-eta', h.eta);
    setText('hud-pace', h.eta ? (h.eta_pace || '') : '');
    var off = $('hud-offroute');
    if (off) {
      off.textContent = h.off_route ? 'Off route, ' + h.off_route_miles + ' mi from the plan' : '';
      off.hidden = !h.off_route;
    }
    setText('hud-tz', h.tz);
    var cross = $('hud-cross');
    cross.textContent = h.crossing || '';
    cross.hidden = !h.crossing;
    setText('hud-next', h.has_next ? h.next_name : 'No stop ahead');
    setText('hud-nextmi', h.next_miles);
    var w = $('hud-waze');
    w.hidden = !h.has_next;
    if (h.has_next && h.waze) w.setAttribute('href', h.waze);
    var alt = $('hud-nav-alt');
    alt.hidden = !h.has_next;
    if (h.has_next && h.google) { $('hud-google').setAttribute('href', h.google); $('hud-apple').setAttribute('href', h.apple); }
    paintChips(h);
  }

  function clock() {
    return new Date().toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  }

  // A refresh asked for while one is in flight runs once it returns: the first
  // fix can land during the page-load request, and dropping it left the screen
  // on plan pace until the next ask a minute later.
  function refresh() {
    if (busy) { again = true; return; }
    busy = true;
    again = false;
    var url = root.getAttribute('data-url');
    var q = [];
    if (fix) q.push('lat=' + encodeURIComponent(fix.latitude.toFixed(4)), 'lng=' + encodeURIComponent(fix.longitude.toFixed(4)));
    if (fix && moving) q.push('moving=1');
    if (q.length) url += '?' + q.join('&');
    lastAsk = Date.now();
    fetch(url, { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error('status ' + r.status); return r.json(); })
      .then(function (h) { paint(h); setText('hud-sync', clock()); })
      .catch(function () {
        // Leave the last good picture up; if there is none, say so plainly.
        var ul = $('hud-chips');
        ul.textContent = '';
        var li = document.createElement('li');
        li.className = 'hud-chip unknown';
        li.textContent = 'Hazards: unknown (could not reach the server)';
        ul.appendChild(li);
      })
      .then(function () { busy = false; if (again) refresh(); });
  }

  // --- Find picks: only on a tap, never on a refresh -------------------------------
  var find = $('free-find'), reask = $('free-reask');
  // askPicks is the one request: a plain Find picks, or a re-ask that always
  // asks Chester fresh and carries the driver's own line, if any.
  function askPicks(extra) {
    if (!fix) { setText('free-picks-msg', 'No position fix yet.'); return; }
    find.disabled = true;
    setText('free-picks-msg', ($('hud-aimless') ? 'Looking around you' : 'Looking along the route') + '\u2026 this can take half a minute.');
    var body = new URLSearchParams({ lat: fix.latitude.toFixed(4), lng: fix.longitude.toFixed(4) });
    Object.keys(extra || {}).forEach(function (k) { body.set(k, extra[k]); });
    fetch('/driver/freelance/picks', { method: 'POST', credentials: 'same-origin', body: body,
      headers: { 'Accept': 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error('status ' + r.status); return r.json(); })
      .then(function (o) { if (reask) reask.hidden = false; paintPicks(o); })
      .catch(function () { setText('free-picks-msg', 'Picks unavailable: could not reach the server.'); })
      .then(function () { find.disabled = false; });
  }
  if (find) find.addEventListener('click', function () { askPicks(); });
  if (reask) reask.addEventListener('submit', function (e) {
    e.preventDefault();
    askPicks({ force: '1', want: $('free-want').value });
  });

  function paintPicks(o) {
    var box = $('free-picks');
    box.textContent = '';
    var msg = [];
    if (o.text) msg.push(o.text);
    if (o.note) msg.push(o.note);
    if (o.cached) msg.push('Shown from a recent search of this stretch (places are kept up to a day, Chester\u2019s notes about half an hour). Re-ask Chester for a fresh look.');
    setText('free-picks-msg', msg.join(' '));
    (o.cats || []).forEach(function (c) {
      var sec = document.createElement('section');
      sec.className = 'free-cat';
      var h = document.createElement('h3');
      h.textContent = c.label;
      sec.appendChild(h);
      if (c.state !== 'items') {
        var p = document.createElement('p');
        p.className = 'free-none';
        p.textContent = c.text || 'Unavailable.';
        sec.appendChild(p);
      }
      (c.picks || []).forEach(function (k) {
        var card = document.createElement('div');
        card.className = 'free-pick';
        var n = document.createElement('b');
        n.textContent = k.name;
        card.appendChild(n);
        var meta = [k.town, k.detour].filter(Boolean).join(' \u00b7 ');
        if (meta) { var m = document.createElement('span'); m.className = 'hud-s'; m.textContent = meta; card.appendChild(m); }
        if (k.note) { var t = document.createElement('span'); t.textContent = k.note; card.appendChild(t); }
        var nav = document.createElement('div');
        nav.className = 'nav-alt';
        [['Waze', k.waze, 'btn-primary'], ['Google Maps', k.google, 'btn-outline-dark'], ['Apple Maps', k.apple, 'btn-outline-dark']].forEach(function (l) {
          if (!l[1]) return;
          var a = document.createElement('a');
          a.className = 'btn btn-sm ' + l[2];
          a.setAttribute('href', l[1]);
          a.setAttribute('rel', 'noopener');
          a.textContent = l[0];
          nav.appendChild(a);
        });
        card.appendChild(nav);
        sec.appendChild(card);
      });
      box.appendChild(sec);
    });
  }

  // --- GPS: the browser's own, only if granted ---------------------------------
  var gps = $('hud-gps');
  if (simOn) {
    gps.textContent = 'simulated (server)';
  } else if (!('geolocation' in navigator)) {
    gps.textContent = 'not available';
  } else {
    navigator.geolocation.watchPosition(function (p) {
      var first = !fix;
      fix = p.coords;
      var cur = { lat: p.coords.latitude, lng: p.coords.longitude, t: Date.now(), speed: p.coords.speed };
      hist.push(cur);
      while (hist.length > 1 && cur.t - hist[0].t > 120000) hist.shift();
      moving = isMoving(hist[0] === cur ? null : hist[0], cur);
      gps.textContent = 'fix, within ' + Math.round(p.coords.accuracy * 3.28084) + ' ft';
      if (first || Date.now() - lastAsk > 60000) refresh();
    }, function (e) {
      send('geo_error', { code: (e && e.code) | 0 });
      gps.textContent = e && e.code === 1 ? 'off (not allowed)' : 'no fix';
    }, { enableHighAccuracy: true, maximumAge: 15000, timeout: 20000 });
  }

  // --- online / offline ---------------------------------------------------------
  function net() {
    var on = navigator.onLine !== false;
    setText('hud-net', on ? 'online' : 'offline');
    if (on && Date.now() - lastAsk > 5000) refresh();
  }
  window.addEventListener('online', net);
  window.addEventListener('offline', net);
  setText('hud-net', navigator.onLine !== false ? 'online' : 'offline');

  // --- RoadTracks: search the music library; a tap opens its player in a new tab ---
  var rtForm = $('rt-form');
  if (rtForm) {
    rtForm.addEventListener('submit', function (e) {
      e.preventDefault();
      var q = ($('rt-q').value || '').trim(), list = $('rt-list');
      list.textContent = '';
      if (!q) { setText('rt-msg', 'Type something to search.'); return; }
      setText('rt-msg', 'Searching\u2026');
      fetch('/driver/roadtracks/search?q=' + encodeURIComponent(q), { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (o) { return { ok: r.ok, o: o }; }); })
        .then(function (x) {
          if (!x.ok) { setText('rt-msg', (x.o && x.o.error) || 'Music library unavailable'); return; }
          var albums = x.o.albums || [];
          setText('rt-msg', albums.length ? '' : 'No albums found.');
          albums.forEach(function (a) {
            var li = document.createElement('li'), l = document.createElement('a');
            l.href = a.url; l.target = '_blank'; l.rel = 'noopener';
            l.textContent = a.album + ' \u2014 ' + a.albumartist + (a.year ? ' (' + a.year + ')' : '');
            li.appendChild(l); list.appendChild(li);
          });
        })
        .catch(function () { setText('rt-msg', 'Music library unavailable'); });
    });
  }

  // --- telemetry: nav taps, script errors, the page view -------------------------
  // The tap is sent at once (a beacon survives the page going away); how it
  // came out follows: nav_result hidden:true if the page went hidden within
  // 1.5 s (the phone opened the app), hidden:false if it did not.
  var tapped = null;
  ['hud-waze', 'hud-google', 'hud-apple', 'free-waze', 'free-google', 'free-apple'].forEach(function (id) {
    var el = $(id);
    if (!el) return;
    var app = id.split('-')[1];
    el.addEventListener('click', function () {
      if (tapped) return;
      var t = { app: app, mode: $('hud-free') ? 'destined' : 'trip', done: false };
      tapped = t;
      sendBody(navTapBody(t.app, t.mode));
      setTimeout(function () {
        if (!t.done) { t.done = true; sendBody(navResultBody(t.app, t.mode, false)); }
        if (tapped === t) tapped = null;
      }, 1500);
    });
  });
  document.addEventListener('visibilitychange', function () {
    if (document.hidden && tapped && !tapped.done) {
      tapped.done = true;
      sendBody(navResultBody(tapped.app, tapped.mode, true));
    }
  });
  window.addEventListener('error', function (e) {
    var src = String((e && e.filename) || '').replace(/^https?:\/\/[^/]+/, '').split(/[?#]/)[0];
    send('js_error', { msg: String((e && e.message) || ''), src: src, line: (e && e.lineno) | 0 });
  });
  send('page_view', { path: (window.location && window.location.pathname) || '/driver' });

  paintClock();
  setInterval(paintClock, 20000);
  refresh();
  setInterval(function () { if (!document.hidden && navigator.onLine !== false) refresh(); }, simOn ? 15000 : 120000);
})();
