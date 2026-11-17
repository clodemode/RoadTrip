/* roadtrip — map (the Ride and a leg).
 *
 * The route (leg polylines in a colour cycle, and the driven track over it) is
 * always drawn. On top of it go three kinds of pin the visitor can switch with
 * plain buttons on the map: Music (music notes), Photos (posts with a photo) and
 * Notes (posts without one). There are no day, start, checkpoint, sight or hours
 * markers: the checkpoints list under the map says that. Plain Leaflet.
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

  var all = [];   // every latlng the route draws (the leg lines and the driven track), for the initial fit
  var cpPts = []; // the checkpoints: not drawn, they only stand in for the fit when the route is empty
  (data.checkpoints || []).forEach(function (c) { cpPts.push([c.lat, c.lng]); });
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

  /* --- posts and music notes: one gathering group -------------------------
     Photo pins (34 px) and music notes (30 px) that would sit on top of each other
     gather into one round count; a tap on the count zooms in place to spread them.
     The radius is a little more than a photo pin, so two pins that touch always
     gather. Each kind keeps its own list of markers: a button takes them out of the
     group (and out of every count) or puts them back. */
  var gather = L.markerClusterGroup({
    maxClusterRadius: 45, showCoverageOnHover: false, spiderfyOnMaxZoom: true, zoomToBoundsOnClick: true,
    iconCreateFunction: function (c) {
      return L.divIcon({ className: 'rt-gather', iconSize: [40, 40], html: '<span>' + c.getChildCount() + '</span>' });
    }
  }).addTo(map);
  var kinds = { music: { on: true, marks: [] }, photos: { on: true, marks: [] }, notes: { on: true, marks: [] } };
  function place(kind, m) {
    kinds[kind].marks.push(m);
    if (kinds[kind].on) gather.addLayer(m);
  }
  (data.posts || []).forEach(function (p) {
    var html = p.thumb ? '<img src="' + esc(p.thumb) + '" alt="">' : '<span>&#9679;</span>';
    var m = L.marker([p.lat, p.lng], {
      icon: L.divIcon({ className: 'post-pin' + (p.thumb ? ' has-photo' : ''),
        iconSize: [34, 34], iconAnchor: [17, 17], html: html }),
      keyboard: false, zIndexOffset: 1000
    });
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
    place(p.thumb ? 'photos' : 'notes', m);
    postPts.push([p.lat, p.lng]);
  });

  /* --- music notes (nowplaying.js, text only) ----------------------------
     Each play with a published point is a music note on the map. The Listen
     Inn itself is a card of its own, off the map (listeninn.js); it asks
     /tracks.json once a minute and tells this map what it read. */
  var NP = window.RTNowPlaying;
  var drawPlays = function (doc) {
    gather.removeLayers(kinds.music.marks);
    kinds.music.marks = [];
    ((doc && doc.tracks) || []).forEach(function (p) {
      if (typeof p.lat !== 'number' || typeof p.lng !== 'number' || !isFinite(p.lat) || !isFinite(p.lng)) return;
      /* A note is drawn as a pin whose tip is the spot, 30 px across, so it can be
         seen and tapped beside a photo (1000); it sits under the photos. */
      var note = L.marker([p.lat, p.lng], {
        icon: L.divIcon({ className: 'np-pin', iconSize: [30, 30], iconAnchor: [15, 34], html: '<span>&#9834;</span>' }),
        keyboard: false, zIndexOffset: 700
      }).bindPopup('<div class="popup">' + NP.rowHtml(p, esc) + '</div>');
      place('music', note);
    });
  };
  if (NP && window.RIDE_TRACKS) {
    drawPlays(window.RIDE_TRACKS);
    document.addEventListener('rt:tracks', function (e) { if (e && e.detail) drawPlays(e.detail); });
  }

  /* Music, Photos and Notes: three plain buttons in place of a layers menu. The route has none. */
  RTMap.toggles(map, [['music', 'Music'], ['photos', 'Photos'], ['notes', 'Notes']].map(function (k) {
    return { key: k[0], label: k[1], set: function (on) {
      var kind = kinds[k[0]];
      kind.on = on;
      if (on) gather.addLayers(kind.marks); else gather.removeLayers(kind.marks);
    } };
  }));

  /* --- fit --------------------------------------------------------------- */
  /* A leg's map opens on the leg: its line and its driven track. A post pinned
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
  /* The checkpoints are not drawn; they only keep the map from opening blank when
     there is no route at all. */
  if (!all.length) fitPts = cpPts.concat(postPts);
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
  refit();
  RTMap.controls(map, el, {
    fitLabel: el.dataset.mode === 'leg' ? 'Fit leg' : 'Fit trip',
    fit: function () { userMoved = false; refit(); }
  });
  if (window.ResizeObserver) {
    new ResizeObserver(refit).observe(el);
  } else {
    window.addEventListener('load', refit);
    window.addEventListener('resize', refit);
  }
})();
