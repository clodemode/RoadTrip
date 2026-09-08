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

  /* --- the actual track -------------------------------------------------- */
  /* Where the car is known to have gone: the arrived origin, the leg's pinned
     posts in the order they happened, the arrived terminus. The server only
     builds one for a leg that has some of that, so an untravelled leg shows
     its plan alone. */
  var trackLayer = L.layerGroup().addTo(map);
  (data.tracks || []).forEach(function (t) {
    var pts = (t.line || []).map(function (c) { return [c[1], c[0]]; });
    if (pts.length < 2) return;
    var line = L.polyline(pts, { color: t.color, weight: 5, opacity: 0.95 }).addTo(trackLayer);
    line.bindPopup('<div class="popup"><h3>Leg ' + t.leg + ' &middot; driven</h3><p>' + esc(t.name) +
      '</p><p class="muted">the actual track</p><p><a href="/legs/' + esc(t.slug) +
      '">Open leg &rarr;</a></p></div>');
    all = all.concat(pts);
  });

  /* --- checkpoints ------------------------------------------------------ */
  var cpLayer = L.layerGroup().addTo(map);
  (data.checkpoints || []).forEach(function (c) {
    var m = L.marker([c.lat, c.lng], {
      icon: L.divIcon({ className: 'cp-pin' + (c.arrived ? ' arrived' : ''),
        iconSize: [26, 26], iconAnchor: [13, 13], html: '<span>' + c.ord + '</span>' }),
      keyboard: false, zIndexOffset: 500
    }).addTo(cpLayer);
    var bits = ['<div class="popup"><h3>' + c.ord + '. ' + esc(c.name) + '</h3>'];
    if (c.label) bits.push('<p>' + esc(c.label) + '</p>');
    if (c.precision === 'fuzzed') bits.push('<p class="muted">approximate position</p>');
    if (c.arrived) bits.push('<p><b>Arrived</b> ' + esc(c.arrived) + '</p>');
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
    if (p.thumb) bits.push('<a href="' + esc(p.full) + '"><img class="popup-photo" src="' + esc(p.thumb) + '" alt=""></a>');
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

  L.control.layers(null, {
    'Route': legLayer, 'Driven': trackLayer, 'Checkpoints': cpLayer,
    'Candidate stops': sightLayer, 'Posts': postLayer
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
