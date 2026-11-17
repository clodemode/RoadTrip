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
  // A page drawn from a past or next trip (?ride=) keeps its leg links on it.
  var rideQ = (function () {
    var m = /[?&]ride=([a-z0-9-]+)/.exec((typeof location !== 'undefined' && location.search) || '');
    return m ? '?ride=' + m[1] : '';
  })();

  // The options and the buttons every map shares come from map_common.js: one
  // finger scrolls the page on a touch screen, two move the map.
  var map = RTMap.create(el);

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  var all = [];   // every latlng the route and its stops draw, for the initial fit
  var postPts = [];   // the pinned posts: they join the fit only when they belong to the view (see the fit)

  /* --- legs ------------------------------------------------------------- */
  /* A leg that has an actual track drawn over it gives up the solid line: the
     plan goes dashed and dim so the two never compete for the same reading. */
  var driven = {};
  (data.tracks || []).forEach(function (t) { driven[t.leg] = true; });

  var legLayer = L.layerGroup().addTo(map);
  var legRuns = [];
  (data.legs || []).forEach(function (leg) {
    var pts = (leg.line || []).map(function (c) { return [c[1], c[0]]; });
    if (pts.length) legRuns.push({ leg: leg, pts: pts });
  });
  /* A white casing under each line, drawn first for every leg so no casing ever
     paints over a neighbour's line: the route stays readable on the busy tiles at
     trip zoom. A leg whose actual track is drawn over it stays a dim dashed plan. */
  legRuns.forEach(function (r) {
    if (!driven[r.leg.ord]) L.polyline(r.pts, { color: '#fff', weight: 8, opacity: 0.9, interactive: false }).addTo(legLayer);
  });
  legRuns.forEach(function (r) {
    var leg = r.leg, pts = r.pts;
    var style = driven[leg.ord]
      ? { color: leg.color, weight: 3, opacity: 0.35, dashArray: '6,8' }
      : { color: leg.color, weight: 4, opacity: 0.85 };
    var line = L.polyline(pts, style).addTo(legLayer);
    line.bindPopup('<div class="popup"><h3>Leg ' + leg.ord + '</h3><p>' + esc(leg.name) +
      '</p><p class="muted">' + esc(leg.date) + (driven[leg.ord] ? ' &middot; planned' : '') +
      '</p><p><a href="/legs/' + esc(leg.slug) + rideQ + '">Open leg &rarr;</a></p></div>');
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
  var trackRuns = [];
  (data.tracks || []).forEach(function (t) {
    var pts = (t.line || []).map(function (c) { return [c[1], c[0]]; });
    if (pts.length >= 2) trackRuns.push({ t: t, pts: pts });
  });
  trackRuns.forEach(function (r) {
    L.polyline(r.t.planned_start ? r.pts.slice(1) : r.pts, { color: '#fff', weight: 8, opacity: 0.9, interactive: false }).addTo(trackLayer);
  });
  trackRuns.forEach(function (r) {
    var t = r.t, pts = r.pts;
    /* planned_start: the first vertex is where the leg was planned to begin,
       not a place the ride was stamped at. That first stretch is drawn lighter
       and dashed, so the line never claims more than is known. */
    var solid = pts, popup = '<div class="popup"><h3>Leg ' + t.leg + ' &middot; driven</h3><p>' + esc(t.name) +
      '</p><p class="muted">the actual track</p><p><a href="/legs/' + esc(t.slug) + rideQ +
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
  var dayPins = [];
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
      if (isDayEnd) dayPins.push({ m: m, name: c.name });
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

  /* --- posts and music notes: one gathering group -------------------------
     Photo pins (34 px) and music notes (30 px) that would sit on top of each other
     gather into one round count; a tap on the count zooms in place to spread them.
     Checkpoints and day pins are never in this group. The radius is a little more
     than a photo pin, so two pins that touch always gather. */
  var gather = L.markerClusterGroup({
    maxClusterRadius: 45, showCoverageOnHover: false, spiderfyOnMaxZoom: true, zoomToBoundsOnClick: true,
    iconCreateFunction: function (c) {
      return L.divIcon({ className: 'rt-gather', iconSize: [40, 40], html: '<span>' + c.getChildCount() + '</span>' });
    }
  }).addTo(map);
  var postLayer = gather;
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
    postPts.push([p.lat, p.lng]);
  });

  /* --- music notes (nowplaying.js, text only) ----------------------------
     Each play with a published point is a music note on the map. The Listen
     Inn itself is a card of its own, off the map (listeninn.js); it asks
     /tracks.json once a minute and tells this map what it read. */
  var NP = window.RTNowPlaying;
  var playLayer = gather;
  var notes = [];
  var drawPlays = function (doc) {
    gather.removeLayers(notes);
    notes = [];
    ((doc && doc.tracks) || []).forEach(function (p) {
      if (typeof p.lat !== 'number' || typeof p.lng !== 'number' || !isFinite(p.lat) || !isFinite(p.lng)) return;
      /* A track often starts at a stop, so a note sits above the stop pins
         (500 and 600) and under a photo (1000); it is drawn as a pin whose tip
         is the spot, 30 px across, so it can be seen and tapped beside the
         stop's own circle. */
      var note = L.marker([p.lat, p.lng], {
        icon: L.divIcon({ className: 'np-pin', iconSize: [30, 30], iconAnchor: [15, 34], html: '<span>&#9834;</span>' }),
        keyboard: false, zIndexOffset: 700
      }).addTo(gather).bindPopup('<div class="popup">' + NP.rowHtml(p, esc) + '</div>');
      notes.push(note);
    });
  };
  if (NP && window.RIDE_TRACKS) {
    drawPlays(window.RIDE_TRACKS);
    document.addEventListener('rt:tracks', function (e) { if (e && e.detail) drawPlays(e.detail); });
  }

  L.control.layers(null, {
    'Route': legLayer, 'Driven': trackLayer, 'Checkpoints': cpLayer,
    'Candidate stops': sightLayer, 'Posts and music': gather
  }, { collapsed: true }).addTo(map);

  /* --- fit --------------------------------------------------------------- */
  /* A leg's map opens on the leg: its line and its checkpoints. A post pinned
     well off that stretch (a stray fix, a photo taken from a detour) stays on the
     map but does not stretch the opening view out to it; the Ride keeps every post. */
  var fitPts = all.slice();
  if (el.dataset.mode === 'leg' && all.length) {
    var S = 90, N = -90, W = 180, E = -180, M = 0.25;
    all.forEach(function (q) { S = Math.min(S, q[0]); N = Math.max(N, q[0]); W = Math.min(W, q[1]); E = Math.max(E, q[1]); });
    postPts.forEach(function (q) { if (q[0] >= S - M && q[0] <= N + M && q[1] >= W - M && q[1] <= E + M) fitPts.push(q); });
  } else {
    fitPts = all.concat(postPts);
  }
  var bounds = fitPts.length ? L.latLngBounds(fitPts) : L.latLngBounds([[25, -125], [45, -95]]);

  /* Leaflet measures the container once, at construction. If the layout has
     not settled the map can be a couple of pixels wide and fitBounds clamps
     to maxZoom — a fully zoomed-in map showing nothing. Re-fit on resize until
     the visitor really takes the view: a drag or a zoom. A finger that only
     touches the map to scroll the page, a wheel turn or a click is not taking
     it, and used to freeze a stale view (the destination off-screen). The
     map's own fit is animate:false, so its events arrive inside the call and
     are told apart from the visitor's by the flag. */
  var userMoved = false, fitting = false;
  map.on('dragstart', function () { userMoved = true; });
  map.on('zoomstart', function () { if (!fitting) userMoved = true; });
  function refit() {
    map.invalidateSize();
    if (userMoved) return;
    fitting = true;
    try { map.fitBounds(bounds, { padding: [24, 24], animate: false }); } finally { fitting = false; }
  }
  /* A night's town name is hidden when it would sit on another pin or name. */
  function declutterNames() {
    var badges = dayPins.map(function (d) {
      var p = map.latLngToLayerPoint(d.m.getLatLng());
      return { x0: p.x - 16, x1: p.x + 16, y0: p.y - 14, y1: p.y + 14, p: p, d: d };
    });
    var shown = [];
    function hit(a, b) { return a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1; }
    badges.forEach(function (b) {
      var icon = b.d.m._icon, n = icon && icon.querySelector ? icon.querySelector('.day-pin-n') : null;
      if (!n) return;
      var half = (String(b.d.name).length * 6.5 + 8) / 2;
      var box = { x0: b.p.x - half, x1: b.p.x + half, y0: b.p.y + 4, y1: b.p.y + 22 };
      var clash = badges.some(function (o) { return o !== b && hit(box, o); }) || shown.some(function (o) { return hit(box, o); });
      n.classList.toggle('rt-name-off', clash);
      if (!clash) shown.push(box);
    });
  }
  map.on('zoomend', declutterNames);

  refit();
  declutterNames();
  RTMap.controls(map, el, {
    fitLabel: el.dataset.mode === 'leg' ? 'Fit leg' : 'Fit trip',
    fit: function () { userMoved = false; refit(); declutterNames(); }
  });
  if (window.ResizeObserver) {
    new ResizeObserver(refit).observe(el);
  } else {
    window.addEventListener('load', refit);
    window.addEventListener('resize', refit);
  }
})();
