/* roadtrip — the planner.
 *
 * window.PLAN is {doc, choices, start, saved} from the server. doc carries
 * every road the plan could take: a leg with no "when" is part of every plan,
 * a leg with one belongs to the plans where every option it names holds the
 * choice named. Checkpoints work the same way. An option may itself carry a
 * "when": it is in force, and its fieldset shown, only while the choice it
 * names is selected. Every coordinate in doc has already been through the store's fuzz
 * seam — a private place is its snapped cell and no line runs to its door —
 * so nothing here needs to, or may, know a real one.
 *
 * Switching a choice re-does the arithmetic in the page: miles per day, hours
 * at the plan's average speed, the long-day flag, the totals. It is the same
 * sum the server does for the first render and for /plan.json. A switch is
 * local to this browser; only the trip-keeper's Save posts it.
 */
(function () {
  'use strict';

  document.documentElement.classList.add('js');

  var P = window.PLAN || {};
  var doc = P.doc || { options: [], legs: [], checkpoints: [] };
  var state = { choices: {}, start: P.start || doc.start_date || '' };
  Object.keys(P.choices || {}).forEach(function (k) { state.choices[k] = P.choices[k]; });

  var form = document.getElementById('plan-form');
  var daysEl = document.getElementById('plan-days');
  var allBtn = document.getElementById('plan-all');
  var mapEl = document.getElementById('map');

  /* Which options are in force under the current choices: one with a "when"
     only while every option it names is in force and holds the choice named.
     Options are declared in order and name only earlier ones. Recomputed at
     each render, read by taken(). */
  var force = {};
  function refreshForce() {
    force = {};
    (doc.options || []).forEach(function (o) {
      var on = true;
      Object.keys(o.when || {}).forEach(function (k) { if (!force[k] || state.choices[k] !== o.when[k]) on = false; });
      force[o.id] = on;
    });
  }
  function taken(f) {
    var w = f.when;
    if (!w) return true;
    return Object.keys(w).every(function (k) { return force[k] && state.choices[k] === w[k]; });
  }
  function round1(v) { return Math.round(v * 10) / 10; }
  function fixed1(v) { return round1(v).toFixed(1); }
  function thousands(n) { return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ','); }
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  var WD = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  var MO = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  /* A leg's date is a calendar day, not an instant: it is worked in UTC so the
     viewer's own zone can never push it across midnight. */
  function dayDate(day, fixedDate) {
    var iso = fixedDate || state.start;
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || '');
    if (!m) return '';
    var t = Date.UTC(+m[1], +m[2] - 1, +m[3]) + (fixedDate ? 0 : (day - 1) * 86400000);
    var d = new Date(t);
    return WD[d.getUTCDay()] + ' ' + MO[d.getUTCMonth()] + ' ' + d.getUTCDate();
  }

  /* --- the arithmetic ---------------------------------------------------- */
  function summarize() {
    var byDay = {}, order = [], total = 0;
    doc.legs.slice().sort(function (a, b) { return a.ord - b.ord; }).forEach(function (l) {
      if (!taken(l)) return;
      var d = byDay[l.day];
      if (!d) {
        d = byDay[l.day] = { day: l.day, date: dayDate(l.day, l.date), name: '', miles: 0, hours: 0,
          color: l.color, legs: [] };
        order.push(l.day);
      }
      d.legs.push(l);
      d.name += (d.name ? ', then ' : '') + l.name;
      d.miles += l.miles;
      d.hours += doc.avg_mph > 0 ? l.miles / doc.avg_mph : l.hours;
      total += l.miles;
    });
    order.sort(function (a, b) { return a - b; });
    var hours = 0;
    var days = order.map(function (n) {
      var d = byDay[n];
      d.long = doc.long_day_hours > 0 && d.hours > doc.long_day_hours;
      hours += d.hours;
      return d;
    });
    return { days: days, miles: round1(total), hours: round1(hours) };
  }

  /* --- the map ------------------------------------------------------------ */
  var map = null, lines = [], cpLayer = null, allBounds = null, userMoved = false, focusDay = 0;

  if (mapEl && typeof L !== 'undefined') {
    map = L.map('map', { scrollWheelZoom: false, maxZoom: 18 });
    mapEl._leafletMap = map;
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      attribution: '&copy; OpenStreetMap contributors', maxZoom: 19
    }).addTo(map);
    L.control.scale({ imperial: true, metric: true }).addTo(map);
    cpLayer = L.layerGroup().addTo(map);

    var every = [];
    doc.legs.forEach(function (leg) {
      var pts = (leg.line || []).map(function (c) { return [c[1], c[0]]; });
      if (pts.length < 2) return;
      var line = L.polyline(pts, {}).addTo(map);
      /* A wide, nearly invisible line over the drawn one: a 3px dashed line is
         not something a thumb can hit, and this is how an alternative is
         chosen from the map. */
      var hit = L.polyline(pts, { weight: 26, opacity: 0.01, color: '#000' }).addTo(map);
      hit.on('click', function () {
        if (leg.when && !taken(leg)) { chooseAll(leg.when); }
        else { zoomDay(leg.day); }
      });
      lines.push({ leg: leg, line: line, hit: hit, pts: pts });
      every = every.concat(pts);
    });
    doc.checkpoints.forEach(function (c) { every.push([c.lat, c.lng]); });
    allBounds = every.length ? L.latLngBounds(every) : L.latLngBounds([[25, -125], [50, -65]]);

    ['mousedown', 'wheel', 'touchstart', 'dblclick'].forEach(function (ev) {
      mapEl.addEventListener(ev, function () { userMoved = true; }, { passive: true });
    });
    var refit = function () {
      map.invalidateSize();
      if (userMoved || focusDay) return;
      map.fitBounds(allBounds, { padding: [24, 24] });
    };
    refit();
    if (window.ResizeObserver) { new ResizeObserver(refit).observe(mapEl); }
    else { window.addEventListener('load', refit); window.addEventListener('resize', refit); }
  }

  function drawMap() {
    if (!map) return;
    lines.forEach(function (x) {
      var on = taken(x.leg);
      x.line.setStyle(on
        ? { color: x.leg.color, weight: 5, opacity: 0.9, dashArray: null }
        : { color: '#6b7280', weight: 3, opacity: 0.8, dashArray: '6,8' });
      if (on) { x.line.bringToFront(); }
      x.hit.bringToFront();
      x.hit.unbindTooltip();
      /* Leaflet renders a string tooltip as HTML, and a leg name is text from
         the trip document: it goes through esc() like every popup below. */
      x.hit.bindTooltip(on ? ('Day ' + x.leg.day + ': ' + esc(x.leg.name))
        : ('Switch to this route: ' + fixed1(x.leg.miles) + ' mi'), { sticky: true });
    });
    cpLayer.clearLayers();
    doc.checkpoints.forEach(function (c) {
      if (!taken(c)) return;
      var m = L.marker([c.lat, c.lng], {
        icon: L.divIcon({ className: 'cp-pin', iconSize: [26, 26], iconAnchor: [13, 13],
          html: '<span>' + c.ord + '</span>' }),
        keyboard: false, zIndexOffset: 500
      }).addTo(cpLayer);
      m.bindPopup('<div class="popup"><h3>' + c.ord + '. ' + esc(c.name) + '</h3>' +
        (c.label ? '<p>' + esc(c.label) + '</p>' : '') +
        (c.precision === 'fuzzed' ? '<p class="muted">approximate position</p>' : '') +
        '<p class="muted">' + esc(c.tz) + '</p></div>');
    });
  }

  function zoomDay(day) {
    focusDay = day;
    if (map) {
      var pts = [];
      lines.forEach(function (x) { if (taken(x.leg) && x.leg.day === day) pts = pts.concat(x.pts); });
      if (pts.length) { map.fitBounds(L.latLngBounds(pts), { padding: [28, 28] }); }
    }
    if (allBtn) allBtn.hidden = false;
    markDay();
  }

  function zoomAll() {
    focusDay = 0;
    userMoved = false;
    if (map) map.fitBounds(allBounds, { padding: [24, 24] });
    if (allBtn) allBtn.hidden = true;
    markDay();
  }

  function markDay() {
    if (!daysEl) return;
    Array.prototype.forEach.call(daysEl.querySelectorAll('.day-row'), function (b) {
      var on = +b.getAttribute('data-day') === focusDay;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
  }

  /* --- the page ------------------------------------------------------------ */
  function drawDays(sum) {
    if (daysEl) {
      daysEl.innerHTML = sum.days.map(function (d) {
        return '<li><button type="button" class="day-row" data-day="' + d.day + '" style="--leg:' + esc(d.color) + '">' +
          '<span class="day-n">' + d.day + '</span>' +
          '<span class="day-main"><span class="day-name">' + esc(d.name) + '</span>' +
          '<span class="day-date muted">' + esc(d.date) + '</span></span>' +
          '<span class="day-nums"><span class="day-miles">' + fixed1(d.miles) + ' mi</span>' +
          '<span class="day-hours muted">' + fixed1(d.hours) + ' h</span>' +
          (d.long ? '<span class="pill pill-long">Long day</span>' : '') + '</span></button></li>';
      }).join('');
    }
    var miles = document.getElementById('plan-miles');
    var count = document.getElementById('plan-daycount');
    var hours = document.getElementById('plan-hours');
    if (miles) miles.textContent = thousands(Math.round(sum.miles)) + ' mi';
    if (count) count.textContent = sum.days.length + (sum.days.length === 1 ? ' day' : ' days');
    if (hours) hours.textContent = '· about ' + fixed1(sum.hours) + ' h of driving';
  }

  function syncURL() {
    if (!window.history || !history.replaceState) return;
    var q = [];
    Object.keys(state.choices).sort().forEach(function (k) {
      q.push('opt-' + encodeURIComponent(k) + '=' + encodeURIComponent(state.choices[k]));
    });
    if (state.start) q.push('start=' + encodeURIComponent(state.start));
    history.replaceState(null, '', location.pathname + (q.length ? '?' + q.join('&') : ''));
  }

  function render() {
    refreshForce();
    showOptions();
    var sum = summarize();
    /* A day that the new selection no longer has cannot stay in focus. */
    if (focusDay && !sum.days.some(function (d) { return d.day === focusDay; })) { focusDay = 0; }
    drawDays(sum);
    drawMap();
    markDay();
    if (allBtn) allBtn.hidden = !focusDay;
  }

  /* chooseAll takes every choice in pairs at once: a road on the map may be
     tied to two options, and switching to it means switching both. */
  function chooseAll(pairs) {
    Object.keys(pairs).forEach(function (k) { state.choices[k] = pairs[k]; });
    if (form) {
      Array.prototype.forEach.call(form.querySelectorAll('input[type=radio]'), function (r) {
        var k = r.name.slice(4);
        if (Object.prototype.hasOwnProperty.call(pairs, k)) r.checked = r.value === pairs[k];
      });
    }
    /* A message from the last save is about a plan this is no longer. */
    Array.prototype.forEach.call(document.querySelectorAll('.plan-msg'), function (m) { m.hidden = true; });
    syncURL();
    render();
  }

  function choose(option, choice) {
    var pairs = {};
    pairs[option] = choice;
    chooseAll(pairs);
  }

  /* An option not in force leaves the form; its radios keep their value, so
     switching back brings the same choice with it. */
  function showOptions() {
    Array.prototype.forEach.call(document.querySelectorAll('.plan-opt'), function (fs) {
      var id = (fs.id || '').replace(/^opt-/, '');
      if (id && Object.prototype.hasOwnProperty.call(force, id)) fs.hidden = !force[id];
    });
  }

  if (form) {
    form.addEventListener('change', function (e) {
      var t = e.target;
      if (t && t.type === 'radio' && t.name.indexOf('opt-') === 0) {
        choose(t.name.slice(4), t.value);
      } else if (t && t.name === 'start') {
        state.start = t.value;
        syncURL();
        render();
      }
    });
  }
  if (daysEl) {
    daysEl.addEventListener('click', function (e) {
      var b = e.target.closest ? e.target.closest('.day-row') : null;
      if (!b) return;
      var day = +b.getAttribute('data-day');
      if (day === focusDay) { zoomAll(); } else { zoomDay(day); }
      if (mapEl && mapEl.scrollIntoView && mapEl.getBoundingClientRect().top < 0) {
        mapEl.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }
    });
  }
  if (allBtn) allBtn.addEventListener('click', zoomAll);

  render();
})();
