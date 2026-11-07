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
  var root = document.getElementById('hud');
  if (!root) return;
  var $ = function (id) { return document.getElementById(id); };
  var fix = null, lastAsk = 0, busy = false;

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

  function paint(h) {
    if (h.free) { paintChips(h); paintFree(h.free); return; }
    setText('hud-day', h.day_n ? h.day_n + ' of ' + h.day_total : '—');
    setText('hud-leg', h.leg_name);
    setText('hud-done', h.miles_done);
    setText('hud-left', h.miles_left);
    setText('hud-tonight', h.tonight_name);
    setText('hud-eta', h.eta);
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

  function refresh() {
    if (busy) return;
    busy = true;
    var url = root.getAttribute('data-url');
    if (fix) url += '?lat=' + encodeURIComponent(fix.latitude.toFixed(4)) + '&lng=' + encodeURIComponent(fix.longitude.toFixed(4));
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
      .then(function () { busy = false; });
  }

  // --- Find picks: only on a tap, never on a refresh -------------------------------
  var find = $('free-find');
  if (find) {
    find.addEventListener('click', function () {
      if (!fix) { setText('free-picks-msg', 'No position fix yet.'); return; }
      find.disabled = true;
      setText('free-picks-msg', 'Looking along the route\u2026 this can take half a minute.');
      var body = new URLSearchParams({ lat: fix.latitude.toFixed(4), lng: fix.longitude.toFixed(4) });
      fetch('/driver/freelance/picks', { method: 'POST', credentials: 'same-origin', body: body,
        headers: { 'Accept': 'application/json' } })
        .then(function (r) { if (!r.ok) throw new Error('status ' + r.status); return r.json(); })
        .then(paintPicks)
        .catch(function () { setText('free-picks-msg', 'Picks unavailable: could not reach the server.'); })
        .then(function () { find.disabled = false; });
    });
  }

  function paintPicks(o) {
    var box = $('free-picks');
    box.textContent = '';
    var msg = [];
    if (o.text) msg.push(o.text);
    if (o.note) msg.push(o.note);
    if (o.cached) msg.push('Shown from a recent search of this stretch (places are kept up to a day; live music until tonight ends).');
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
  if (!('geolocation' in navigator)) {
    gps.textContent = 'not available';
  } else {
    navigator.geolocation.watchPosition(function (p) {
      var first = !fix;
      fix = p.coords;
      gps.textContent = 'fix, within ' + Math.round(p.coords.accuracy * 3.28084) + ' ft';
      if (first || Date.now() - lastAsk > 60000) refresh();
    }, function (e) {
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

  refresh();
  setInterval(function () { if (!document.hidden && navigator.onLine !== false) refresh(); }, 120000);
})();
