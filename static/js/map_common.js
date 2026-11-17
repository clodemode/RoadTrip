/* roadtrip — what every map shares: the Ride, a leg and the Plan.
 *
 * One place for the options a map is made with and for its buttons, so the three
 * pages never carry three copies. window.RTMap = { create, controls }.
 *
 *  create(el)   the Leaflet map, the tile layer and the scale. On a touch screen
 *               one finger scrolls the PAGE: dragging is off, and two fingers
 *               move and pinch the map (Leaflet's touch zoom pans with the pinch).
 *               A one-finger move shows a short hint over the map, then it fades.
 *               A mouse drags as it always did; the wheel never zooms.
 *  controls()   Fit (the opening view again) and Full screen, with a Done button
 *               and the Escape key to leave it. Full screen is a class on the map,
 *               in place: no dialog, no browser full screen.
 */
(function () {
  'use strict';

  var HINT = 'Use two fingers to move the map';
  var HINT_MS = 1500;

  function coarse() {
    try { if (window.matchMedia) return window.matchMedia('(pointer: coarse)').matches; } catch (e) { /* fall through */ }
    return 'ontouchstart' in window;
  }

  function hint(el) {
    var h = document.createElement('div');
    h.className = 'rt-map-hint';
    h.setAttribute('role', 'status');
    h.textContent = HINT;
    el.appendChild(h);
    var t = null;
    el.addEventListener('touchmove', function (e) {
      if (!e.touches || e.touches.length !== 1) return;   /* two fingers are moving the map: no hint */
      h.classList.add('on');
      if (t) clearTimeout(t);
      t = setTimeout(function () { h.classList.remove('on'); t = null; }, HINT_MS);
    }, { passive: true });
  }

  function create(el) {
    var touch = coarse();
    var map = L.map(el.id || 'map', {
      // Scrolling the page with the pointer over the map must not zoom it.
      scrollWheelZoom: false,
      maxZoom: 18,
      dragging: !touch
    });
    el._leafletMap = map;
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      attribution: '&copy; OpenStreetMap contributors', maxZoom: 19
    }).addTo(map);
    L.control.scale({ imperial: true, metric: true }).addTo(map);
    if (touch) hint(el);
    return map;
  }

  var ICON_FIT = '<svg viewBox="0 0 16 16" width="20" height="20" aria-hidden="true" focusable="false" fill="none" ' +
    'stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><circle cx="8" cy="8" r="3"/>' +
    '<path d="M8 1v3M8 12v3M1 8h3M12 8h3"/></svg>';
  var ICON_FULL = '<svg viewBox="0 0 16 16" width="20" height="20" aria-hidden="true" focusable="false" fill="none" ' +
    'stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">' +
    '<path d="M2 6V2h4M10 2h4v4M14 10v4h-4M6 14H2v-4"/></svg>';

  function button(name, label, html) {
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'rt-map-btn';
    b.setAttribute('data-rt', name);
    b.setAttribute('aria-label', label);
    b.title = label;
    b.innerHTML = html;
    return b;
  }

  /* o.fitLabel: "Fit trip" or "Fit leg"; o.fit(): put the map back on the view the
     page opened with. */
  function controls(map, el, o) {
    var bar = L.control({ position: 'topleft' });
    bar.onAdd = function () {
      var box = document.createElement('div');
      box.className = 'leaflet-bar rt-map-bar';
      var fit = button('fit', o.fitLabel, ICON_FIT);
      var full = button('full', 'Full screen', ICON_FULL);
      var done = button('done', 'Done', 'Done');
      done.hidden = true;
      done.classList.add('rt-map-done');
      function setFull(on) {
        el.classList.toggle('rt-map-full', on);
        document.body.classList.toggle('rt-map-lock', on);
        full.hidden = on;
        done.hidden = !on;
        map.invalidateSize();
        if (on && done.focus) done.focus();
        else if (!on && full.focus) full.focus();
      }
      fit.addEventListener('click', function () { o.fit(); });
      full.addEventListener('click', function () { setFull(true); });
      done.addEventListener('click', function () { setFull(false); });
      document.addEventListener('keydown', function (e) {
        if (e && e.key === 'Escape' && el.classList.contains('rt-map-full')) setFull(false);
      });
      box.appendChild(fit);
      box.appendChild(full);
      box.appendChild(done);
      if (L.DomEvent) { L.DomEvent.disableClickPropagation(box); L.DomEvent.disableScrollPropagation(box); }
      return box;
    };
    bar.addTo(map);
  }

  window.RTMap = { create: create, controls: controls };
})();
