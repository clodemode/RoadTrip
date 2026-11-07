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

  function paint(h) {
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
    var ul = $('hud-chips');
    ul.textContent = '';
    (h.hazards || []).forEach(function (c) {
      var li = document.createElement('li');
      li.className = 'hud-chip ' + c.state;
      li.textContent = c.text;
      ul.appendChild(li);
    });
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
