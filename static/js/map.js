/* roadtrip — map.
 *
 * Leg polylines in a colour cycle, numbered checkpoint pins, muted sight
 * pins, photo pins with a thumbnail popup. Plain Leaflet, no framework.
 * window.RIDE is {legs, tracks, checkpoints, sights, posts} from the server;
 * a leg that has a track is the plan and goes dashed under it. A post
 * carries pin_source (exif | browser | checkpoint) and, for a checkpoint
 * pin, the checkpoint's name as `near`; for a browser pin, accuracy_m.
 * A raw pin taken at a private place is snapped server-side to its fuzzed
 * checkpoint and arrives here as a checkpoint pin: the popup says
 * "near <checkpoint>", never the real position.
 */
(function () {
  'use strict';

  var el = document.getElementById('map');
  if (!el || typeof L === 'undefined') return;
  var data = window.RIDE || { legs: [], checkpoints: [], sights: [], posts: [] };

  var map = L.map('map', {
    // Scrolling the page with the pointer over the map must not zoom it.
    scrollWheelZoom: false,
    maxZoom: 18
  });
  el._leafletMap = map;

  L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
    attribution: '&copy; OpenStreetMap contributors', maxZoom: 19
  }).addTo(map);
  L.control.scale({ imperial: true, metric: true }).addTo(map);

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  var all = [];   // every latlng we draw, for the initial fit

  /* --- legs ------------------------------------------------------------- */
  /* A leg that has an actual track drawn over it gives up the solid line: the
     plan goes dashed and dim so the two never compete for the same reading. */
  var driven = {};
  (data.tracks || []).forEach(function (t) { driven[t.leg] = true; });

  var legLayer = L.layerGroup().addTo(map);
  (data.legs || []).forEach(function (leg) {
    var pts = (leg.line || []).map(function (c) { return [c[1], c[0]]; });
    if (!pts.length) return;
    var style = driven[leg.ord]
      ? { color: leg.color, weight: 3, opacity: 0.35, dashArray: '6,8' }
      : { color: leg.color, weight: 4, opacity: 0.85 };
    var line = L.polyline(pts, style).addTo(legLayer);
    line.bindPopup('<div class="popup"><h3>Leg ' + leg.ord + '</h3><p>' + esc(leg.name) +
      '</p><p class="muted">' + esc(leg.date) + (driven[leg.ord] ? ' &middot; planned' : '') +
      '</p><p><a href="/legs/' + esc(leg.slug) + '">Open leg &rarr;</a></p></div>');
    all = all.concat(pts);
  });

  /* --- the hours stick ---------------------------------------------------
     A thin, muted measuring stick beside the route: the stretch of planned
     line still to drive, drawn HOURS_OFFSET_PX to one side, with a short
     perpendicular tick and a small label ("h1 Trenton") at each whole hour.
     The server sends leg.hours (points ON the planned line) and
     leg.hours_line (the stretch they measure); the offset is computed here in
     screen space and redrawn on zoomend, so it stays 8 px off the road at any
     zoom. Grey and faint on purpose: a ruler, not a second route. */
  var HOURS_OFFSET_PX = 8;
  var HOURS_LABEL_ZOOM = 8;
  var hoursLayer = L.layerGroup().addTo(map);

  function unit(a, b) {
    var dx = b.x - a.x, dy = b.y - a.y, n = Math.sqrt(dx * dx + dy * dy) || 1;
    return { x: dx / n, y: dy / n };
  }
  function drawHours() {
    hoursLayer.clearLayers();
    (data.legs || []).forEach(function (leg) {
      var stretch = (leg.hours_line || []).map(function (c) { return map.latLngToLayerPoint([c[1], c[0]]); });
      if (stretch.length < 2) return;
      // The right-hand normal of each segment; a vertex takes the mean of its two.
      var segN = [];
      for (var i = 1; i < stretch.length; i++) {
        var u = unit(stretch[i - 1], stretch[i]);
        segN.push({ x: -u.y, y: u.x });
      }
      function normalAt(i) {
        var a = segN[Math.max(0, i - 1)], b = segN[Math.min(segN.length - 1, i)];
        var x = a.x + b.x, y = a.y + b.y, n = Math.sqrt(x * x + y * y) || 1;
        return { x: x / n, y: y / n };
      }
      function off(p, n, d) { return map.layerPointToLatLng(L.point(p.x + n.x * d, p.y + n.y * d)); }
      var side = stretch.map(function (p, i) { return off(p, normalAt(i), HOURS_OFFSET_PX); });
      L.polyline(side, { color: '#6b7280', weight: 2, opacity: 0.5, interactive: false }).addTo(hoursLayer);
      (leg.hours || []).forEach(function (h) {
        var p = map.latLngToLayerPoint([h.lat, h.lng]);
        // The segment the tick sits on: the nearest one.
        var best = 0, bd = Infinity;
        for (var i = 1; i < stretch.length; i++) {
          var a = stretch[i - 1], b = stretch[i], dx = b.x - a.x, dy = b.y - a.y, l2 = dx * dx + dy * dy || 1;
          var t = Math.max(0, Math.min(1, ((p.x - a.x) * dx + (p.y - a.y) * dy) / l2));
          var d = Math.pow(a.x + t * dx - p.x, 2) + Math.pow(a.y + t * dy - p.y, 2);
          if (d < bd) { bd = d; best = i - 1; }
        }
        var n = segN[best];
        L.polyline([off(p, n, HOURS_OFFSET_PX - 5), off(p, n, HOURS_OFFSET_PX + 5)],
          { color: '#6b7280', weight: 2, opacity: 0.5, interactive: false }).addTo(hoursLayer);
        /* Zoomed out, a whole trip's hour names pile on top of each other and
           of the pins: the ticks stay, the names wait for a closer look. */
        if (map.getZoom() < HOURS_LABEL_ZOOM) return;
        L.marker(off(p, n, HOURS_OFFSET_PX + 12), { interactive: false, keyboard: false,
          icon: L.divIcon({ className: 'hours-lbl', iconSize: null,
            html: '<span>' + esc(h.label + (h.name ? ' ' + h.name : '')) + '</span>' }) }).addTo(hoursLayer);
      });
    });
  }
  map.whenReady(drawHours);   /* after the first fit: projection needs a view */
  map.on('zoomend', drawHours);

  /* --- the actual track -------------------------------------------------- */
  /* Where the car is known to have gone: the arrived origin, the leg's pinned
     posts in the order they happened, the arrived terminus. The server only
     builds one for a leg that has some of that, so an untravelled leg shows
     its plan alone. */
  var trackLayer = L.layerGroup().addTo(map);
  (data.tracks || []).forEach(function (t) {
    var pts = (t.line || []).map(function (c) { return [c[1], c[0]]; });
    if (pts.length < 2) return;
    /* planned_start: the first vertex is where the leg was planned to begin,
       not a place the ride was stamped at. That first stretch is drawn lighter
       and dashed, so the line never claims more than is known. */
    var solid = pts, popup = '<div class="popup"><h3>Leg ' + t.leg + ' &middot; driven</h3><p>' + esc(t.name) +
      '</p><p class="muted">the actual track</p><p><a href="/legs/' + esc(t.slug) +
      '">Open leg &rarr;</a></p></div>';
    if (t.planned_start) {
      L.polyline([pts[0], pts[1]], { color: t.color, weight: 5, opacity: 0.6, dashArray: '2,9' })
        .addTo(trackLayer)
        .bindPopup('<div class="popup"><h3>Leg ' + t.leg + '</h3><p>' + esc(t.name) +
          '</p><p class="muted">from the planned start to the first photo</p></div>');
      solid = pts.slice(1);
    }
    if (solid.length >= 2) {
      L.polyline(solid, { color: t.color, weight: 5, opacity: 0.95 }).addTo(trackLayer).bindPopup(popup);
    }
    all = all.concat(pts);
  });

  /* --- checkpoints ------------------------------------------------------ */
  var cpLayer = L.layerGroup().addTo(map);
  var colorBySlug = {};
  (data.legs || []).forEach(function (leg) { colorBySlug[leg.slug] = leg.color; });
  (data.checkpoints || []).forEach(function (c) {
    /* A night's end is marked d1, d2 ...: it wears its day's badge in the day's
       colour with the town beside it. Every other place stays a small pin. */
    var dayMark = /^d\d+$/.test(c.mark || '');
    var isDayEnd = dayMark && !c.is_start;
    var m;
    if (c.is_start && dayMark) {
      /* The start is a small plain dot, like a place passed on the way. */
      m = L.circleMarker([c.lat, c.lng], { radius: 5, color: '#374151', weight: 2, fillColor: '#fff', fillOpacity: 1 }).addTo(cpLayer);
    } else {
      var icon = isDayEnd
        ? L.divIcon({ className: 'day-pin' + (c.arrived ? ' arrived' : '') + (c.skipped ? ' skipped' : ''),
            iconSize: [0, 0], iconAnchor: [0, 0],
            html: '<span class="day-pin-b" style="background:' + esc(colorBySlug[c.leg_slug] || '#0d6efd') + '">' +
              esc(c.mark) + '</span><span class="day-pin-n">' + esc(c.name) + '</span>' })
        : L.divIcon({ className: 'cp-pin' + (c.arrived ? ' arrived' : '') + (c.skipped ? ' skipped' : ''),
            iconSize: [26, 26], iconAnchor: [13, 13], html: '<span>' + esc(c.mark || c.ord) + '</span>' });
      m = L.marker([c.lat, c.lng], { icon: icon, keyboard: false, zIndexOffset: isDayEnd ? 600 : 500 }).addTo(cpLayer);
    }
    var bits = ['<div class="popup"><h3>' + esc(c.mark || c.ord) + '. ' + esc(c.name) + '</h3>'];
    if (c.label) bits.push('<p>' + esc(c.label) + '</p>');
    if (c.precision === 'fuzzed') bits.push('<p class="muted">approximate position</p>');
    if (c.arrived) bits.push('<p><b>' + (c.is_start ? 'Left' : 'Arrived') + '</b> ' + esc(c.arrived) + '</p>');
    else if (c.skipped) bits.push('<p><b>' + (c.skip_reason === 'rerouted' ? 'Rerouted around' : 'Missed') + '</b></p>');
    else if (c.planned) bits.push('<p class="muted">plan: ' + esc(c.planned) + '</p>');
    bits.push('<p class="muted">' + esc(c.tz) + '</p></div>');
    m.bindPopup(bits.join(''));
    all.push([c.lat, c.lng]);
  });

  /* --- sights (muted, off by default on the ride view) ------------------- */
  var sightLayer = L.layerGroup();
  (data.sights || []).forEach(function (s) {
    var m = L.circleMarker([s.lat, s.lng], { radius: 5, color: '#6b7280', weight: 1,
      fillColor: '#9ca3af', fillOpacity: 0.7 }).addTo(sightLayer);
    m.bindPopup('<div class="popup"><h3>' + esc(s.name) + '</h3>' +
      (s.why ? '<p>' + esc(s.why) + '</p>' : '') + '<p class="muted">' + esc(s.kind) + ' &middot; candidate</p></div>');
  });
  if (el.dataset.mode === 'leg') sightLayer.addTo(map);

  /* --- posts ------------------------------------------------------------ */
  var postLayer = L.layerGroup().addTo(map);
  (data.posts || []).forEach(function (p) {
    var html = p.thumb ? '<img src="' + esc(p.thumb) + '" alt="">' : '<span>&#9679;</span>';
    var m = L.marker([p.lat, p.lng], {
      icon: L.divIcon({ className: 'post-pin' + (p.thumb ? ' has-photo' : ''),
        iconSize: [34, 34], iconAnchor: [17, 17], html: html }),
      keyboard: false, zIndexOffset: 1000
    }).addTo(postLayer);
    var bits = ['<div class="popup">'];
    if (p.thumb) bits.push('<a href="/photo/' + p.id + '"><img class="popup-photo" src="' + esc(p.thumb) + '" alt=""></a>');
    bits.push('<h3>' + (p.title ? esc(p.title) : esc(p.kind)) + '</h3>');
    bits.push('<p class="muted">' + esc(p.when) + '</p>');
    if (p.pin_source === 'checkpoint') {
      bits.push('<p class="muted">near ' + esc(p.near || 'the checkpoint') + '</p>');
    } else if (p.pin_source === 'browser') {
      bits.push('<p class="muted">phone location' + (p.accuracy_m ? ' &plusmn;' + Math.round(p.accuracy_m) + ' m' : '') + '</p>');
    }
    bits.push('<p><a href="/#post-' + p.id + '">On the timeline &rarr;</a></p></div>');
    m.bindPopup(bits.join(''), { minWidth: 200 });
    all.push([p.lat, p.lng]);
  });

  /* --- what is playing (nowplaying.js, text only) ------------------------
     The skeleton in the page becomes a small control in the map's corner: the
     "Currently playing" chip and the Listen Inn toggle that opens the list
     over the map's edge. Each play with a published point is a music note. */
  var NP = window.RTNowPlaying, npEl = document.getElementById('np');
  var playLayer = L.layerGroup().addTo(map);
  if (NP && npEl) {
    var npLine = document.getElementById('np-line'), npLabel = document.getElementById('np-label'),
        npPanel = document.getElementById('np-panel'), npBtn = document.getElementById('np-toggle'), npLast = 0;
    var npCtl = L.control({ position: 'bottomleft' });
    npCtl.onAdd = function () { L.DomEvent.disableClickPropagation(npEl); L.DomEvent.disableScrollPropagation(npEl); return npEl; };
    npCtl.addTo(map);
    var drawPlays = function (doc) {
      var t = NP.chipText(doc);
      npEl.hidden = !t;
      if (t) { npLabel.textContent = t.label; npLine.textContent = t.line; }
      npPanel.innerHTML = (doc.tracks || []).map(function (p) { return NP.rowHtml(p, esc, doc.listen && !!document.getElementById('np-audio')); }).join('');
      if (window.RTListen && window.RTListen.update) window.RTListen.update(doc);
      playLayer.clearLayers();
      (doc.tracks || []).forEach(function (p) {
        if (typeof p.lat !== 'number' || typeof p.lng !== 'number') return;
        L.marker([p.lat, p.lng], {
          icon: L.divIcon({ className: 'np-pin', iconSize: [22, 22], iconAnchor: [11, 11], html: '<span>&#9834;</span>' }),
          keyboard: false, zIndexOffset: 400
        }).addTo(playLayer).bindPopup('<div class="popup">' + NP.rowHtml(p, esc) + '</div>');
      });
    };
    npBtn.addEventListener('click', function () {
      var open = npPanel.hidden;
      npPanel.hidden = !open;
      npBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
    drawPlays(window.RIDE_TRACKS || { tracks: [] });
    npLast = Date.now();
    /* Once a minute, and only while the tab is visible: a hidden tab must not
       keep the machine awake. Coming back to the tab asks at once if it is due. */
    var npAsk = function () {
      if (!NP.shouldPoll(document.hidden, npLast, Date.now())) return;
      npLast = Date.now();
      fetch('/tracks.json', { headers: { Accept: 'application/json' } })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (doc) { if (doc) drawPlays(doc); })
        .catch(function () { /* the chip keeps what it had */ });
    };
    setInterval(npAsk, NP.EVERY_MS);
    document.addEventListener('visibilitychange', npAsk);
  }

  L.control.layers(null, {
    'Route': legLayer, 'Driven': trackLayer, 'Checkpoints': cpLayer,
    'Candidate stops': sightLayer, 'Posts': postLayer, 'Music': playLayer
  }, { collapsed: true }).addTo(map);

  /* --- fit --------------------------------------------------------------- */
  var bounds = all.length ? L.latLngBounds(all) : L.latLngBounds([[25, -125], [45, -95]]);

  /* Leaflet measures the container once, at construction. If the layout has
     not settled the map can be a couple of pixels wide and fitBounds clamps
     to maxZoom — a fully zoomed-in map showing nothing. Re-fit on resize until
     the user takes the view. */
  var userMoved = false;
  ['mousedown', 'wheel', 'touchstart', 'dblclick'].forEach(function (ev) {
    el.addEventListener(ev, function () { userMoved = true; }, { passive: true });
  });
  function refit() {
    map.invalidateSize();
    if (userMoved) return;
    map.fitBounds(bounds, { padding: [24, 24] });
  }
  refit();
  if (window.ResizeObserver) {
    new ResizeObserver(refit).observe(el);
  } else {
    window.addEventListener('load', refit);
    window.addEventListener('resize', refit);
  }
})();
