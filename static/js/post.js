/* roadtrip — post form: where the pin comes from, said out loud before you post.
 *
 * Three sources, one privacy toggle:
 *   Photo GPS   — parsed HERE, out of the first 256 KB of the chosen file, so
 *                 the number is on screen before the upload starts. No library
 *                 and no CDN: the site ships one static binary and loads
 *                 nothing from a network it does not serve itself.
 *   My phone    — navigator.geolocation, asked once on load (https; Safari
 *                 prompts once per site). Safari strips EXIF GPS from a web
 *                 camera capture, so on the road this is usually the winner.
 *   Checkpoint  — whatever is picked in the Checkpoint select.
 *
 * "Fuzz location" publishes a 0.05° cell instead of the point. It ticks itself
 * at a private place (window.RT_PRIVATE — the fuzzed checkpoints) and is clear
 * everywhere else, so leaving the form alone protects the home and stays exact
 * on the road. The server decides all of this again for itself; everything
 * here is a display line and a stated preference.
 */
(function () {
  'use strict';

  // --- EXIF: JPEG APP1 -> TIFF -> IFD0 -> GPS IFD ---------------------------
  // Hand-rolled and deliberately small. Anything unexpected returns null and
  // the form says "none in this file" — a parse failure never blocks a post.

  var GPS_IFD = 0x8825, T_LAT_REF = 1, T_LAT = 2, T_LNG_REF = 3, T_LNG = 4;
  // TIFF type sizes by type code: BYTE ASCII SHORT LONG RATIONAL SBYTE …
  var TYPE_SIZE = [0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8];
  var EXIF_MAGIC = 'Exif\u0000\u0000';

  function ascii(dv, off, n) {
    var s = '';
    for (var i = 0; i < n; i++) s += String.fromCharCode(dv.getUint8(off + i));
    return s;
  }

  // exifGPS walks the JPEG segment chain looking for APP1 "Exif\0\0" and
  // returns {lat, lng} in signed decimal degrees, or null.
  function exifGPS(buf) {
    try {
      var dv = new DataView(buf);
      if (dv.byteLength < 4 || dv.getUint16(0) !== 0xFFD8) return null;   // not a JPEG
      var off = 2;
      while (off + 4 <= dv.byteLength) {
        if (dv.getUint8(off) !== 0xFF) { off++; continue; }               // resync
        var marker = dv.getUint8(off + 1);
        if (marker === 0xFF) { off++; continue; }                         // fill byte
        if (marker === 0x01 || (marker >= 0xD0 && marker <= 0xD9)) { off += 2; continue; }
        if (marker === 0xDA) return null;                                 // scan data: no APP1 left
        var len = dv.getUint16(off + 2);
        if (len < 2 || off + 2 + len > dv.byteLength) return null;        // cut off by our slice
        if (marker === 0xE1 && ascii(dv, off + 4, 6) === EXIF_MAGIC) {
          return gpsFromTIFF(dv, off + 10, off + 2 + len);
        }
        off += 2 + len;
      }
    } catch (e) { /* fall through to null — never block the form */ }
    return null;
  }

  function gpsFromTIFF(dv, base, end) {
    if (base + 8 > end) return null;
    var order = dv.getUint16(base);
    if (order !== 0x4949 && order !== 0x4D4D) return null;
    var le = order === 0x4949;                                            // "II" little-endian
    if (dv.getUint16(base + 2, le) !== 42) return null;
    var gps = findLong(dv, base, end, base + dv.getUint32(base + 4, le), le, GPS_IFD);
    if (gps === null) return null;
    var go = base + gps;
    var latRef = tagASCII(dv, base, end, go, le, T_LAT_REF),
        lngRef = tagASCII(dv, base, end, go, le, T_LNG_REF),
        lat = tagDMS(dv, base, end, go, le, T_LAT),
        lng = tagDMS(dv, base, end, go, le, T_LNG);
    if (lat === null || lng === null) return null;
    if (latRef === 'S') lat = -lat;
    if (lngRef === 'W') lng = -lng;
    if (lat < -90 || lat > 90 || lng < -180 || lng > 180) return null;
    if (lat === 0 && lng === 0) return null;                              // the camera meant "unknown"
    return { lat: lat, lng: lng };
  }

  // entry finds one IFD entry by tag and returns {type, count, off} with off
  // absolute in the DataView, or null.
  function entry(dv, base, end, ifd, le, tag) {
    if (ifd < base || ifd + 2 > end) return null;
    var n = dv.getUint16(ifd, le);
    if (ifd + 2 + n * 12 > end) return null;
    for (var i = 0; i < n; i++) {
      var e = ifd + 2 + i * 12;
      if (dv.getUint16(e, le) !== tag) continue;
      var type = dv.getUint16(e + 2, le), count = dv.getUint32(e + 4, le);
      var size = (TYPE_SIZE[type] || 0) * count;
      if (size === 0 || count > 0xFFFF) return null;
      // Up to 4 bytes live in the entry itself; anything larger is an offset
      // from the TIFF header — either can point past the slice we read.
      var vo = size <= 4 ? e + 8 : base + dv.getUint32(e + 8, le);
      if (vo < base || vo + size > end) return null;
      return { type: type, count: count, off: vo };
    }
    return null;
  }

  function findLong(dv, base, end, ifd, le, tag) {
    var e = entry(dv, base, end, ifd, le, tag);
    return e ? dv.getUint32(e.off, le) : null;
  }

  function tagASCII(dv, base, end, ifd, le, tag) {
    var e = entry(dv, base, end, ifd, le, tag);
    if (!e || e.type !== 2) return '';
    return ascii(dv, e.off, e.count).replace(/\u0000+$/, '').trim().toUpperCase();
  }

  // tagDMS reads three RATIONALs (degrees, minutes, seconds) as a positive
  // decimal degree; the sign comes from the matching Ref tag.
  function tagDMS(dv, base, end, ifd, le, tag) {
    var e = entry(dv, base, end, ifd, le, tag);
    if (!e || e.type !== 5 || e.count < 3) return null;
    var out = 0, scale = [1, 60, 3600];
    for (var i = 0; i < 3; i++) {
      var num = dv.getUint32(e.off + i * 8, le), den = dv.getUint32(e.off + i * 8 + 4, le);
      if (den === 0) { if (num !== 0) return null; continue; }
      out += (num / den) / scale[i];
    }
    return out;
  }

  // --- the form -------------------------------------------------------------

  // suggestMilestone returns the id of the nearest offered milestone within
  // 2 km of the point, else null. choices: [{id, lat, lng}]. A suggestion only:
  // the page marks it, the driver still taps it.
  var MS_NEAR_KM = 2.0;
  function haversineKm(aLat, aLng, bLat, bLng) {
    var R = 6371, rad = Math.PI / 180;
    var dLat = (bLat - aLat) * rad, dLng = (bLng - aLng) * rad;
    var h = Math.sin(dLat / 2) * Math.sin(dLat / 2) +
            Math.cos(aLat * rad) * Math.cos(bLat * rad) * Math.sin(dLng / 2) * Math.sin(dLng / 2);
    return 2 * R * Math.asin(Math.sqrt(h));
  }
  function suggestMilestone(point, choices) {
    if (!point || !choices) return null;
    var best = null, bestKm = Infinity;
    for (var i = 0; i < choices.length; i++) {
      var c = choices[i], d = haversineKm(point.lat, point.lng, c.lat, c.lng);
      if (d <= MS_NEAR_KM && d < bestKm) { best = c.id; bestKm = d; }
    }
    return best;
  }

  // askLocation asks geo (navigator.geolocation) for the phone's position at
  // once and reports it as {lat, lng, acc} (lat and lng to 4 dp, acc in
  // metres), or the error. Without a geolocation it reports an error, so the
  // caller's "off" path runs. It returns whether a request was made. fresh
  // asks for a high-accuracy fix that may not come from the cache: the one
  // taken when the page opened can be minutes old by the time a photo is.
  function askLocation(geo, ok, fail, fresh) {
    if (!geo) { fail({ code: 0 }); return false; }
    geo.getCurrentPosition(function (p) {
      var c = p.coords;
      ok({ lat: c.latitude.toFixed(4), lng: c.longitude.toFixed(4), acc: Math.round(c.accuracy || 0) });
    }, fail, { enableHighAccuracy: !!fresh, timeout: 8000, maximumAge: fresh ? 0 : 60000 });
    return true;
  }

  // sectionsFor says which parts of the form a post type needs: the photo
  // upload, and whether the title and body are open (else tucked away as an
  // optional caption). An unknown type shows everything.
  function sectionsFor(kind) {
    if (kind === 'photo') return { upload: true, textOpen: false };
    if (kind === 'note') return { upload: false, textOpen: true };
    return { upload: true, textOpen: true };
  }

  // locationReason is the plain reason the phone's position is not on offer.
  // Browsers give a position only to an https page, so that is said first.
  function locationReason(secure, code) {
    if (secure === false) return 'this page is not HTTPS, so the phone will not share it';
    return code === 1 ? 'denied' : 'no fix';
  }

  if (typeof document === 'undefined') {                                  // node, for the parser check
    if (typeof module !== 'undefined' && module.exports) module.exports = { exifGPS: exifGPS, suggestMilestone: suggestMilestone, askLocation: askLocation, sectionsFor: sectionsFor, locationReason: locationReason };
    return;
  }

  var $ = function (id) { return document.getElementById(id); };
  var photo = $('f-photo'), exSt = $('exif-status'), geoSt = $('geo-status'),
      rExif = $('p-exif'), rBrow = $('p-brow'), rCp = $('p-cp'), rNone = $('p-none'),
      exNote = $('p-exif-note'), cpNote = $('p-cp-note'),
      fuzz = $('f-fuzz'), fuzzSet = $('f-fuzzset'), says = $('pin-publishes'),
      la = $('f-lat'), ln = $('f-lng'), ac = $('f-acc'),
      pla = $('f-plat'), pln = $('f-plng'), cpSel = $('f-cp');
  if (!rExif || !rBrow || !rCp || !rNone || !fuzz || !fuzzSet || !says) return;

  var EXIF_SLICE = 256 * 1024;      // the APP1 segment lives at the head of the file
  var CELL = 0.05;                  // must match store.go fuzzGrid
  var NEAR_KM = 2.0;                // must match store.go privateRadiusKm
  var cells = window.RT_PRIVATE || [];
  var precision = window.RT_PIN_PRECISION || 'snap';
  var exif = null, fix = null, touched = false;

  function snap(v) { return Math.round(Math.round(v / CELL) * CELL * 1000) / 1000; }

  function km(aLat, aLng, bLat, bLng) {
    var R = 6371, rad = Math.PI / 180;
    var dLat = (bLat - aLat) * rad, dLng = (bLng - aLng) * rad;
    var h = Math.sin(dLat / 2) * Math.sin(dLat / 2) +
            Math.cos(aLat * rad) * Math.cos(bLat * rad) * Math.sin(dLng / 2) * Math.sin(dLng / 2);
    return 2 * R * Math.asin(Math.sqrt(h));
  }

  // nearPrivate mirrors store.go nearPrivate. The server decides for itself;
  // if the two ever disagree the server wins and this line was only a guess.
  function nearPrivate(lat, lng) {
    var best = null, bestKm = Infinity;
    for (var i = 0; i < cells.length; i++) {
      var c = cells[i], d = km(lat, lng, c.lat, c.lng);
      var sameCell = snap(lat) === c.lat && snap(lng) === c.lng;
      if (!sameCell && d >= NEAR_KM) continue;
      if (d < bestKm) { best = c; bestKm = d; }
    }
    return best;
  }

  function fmt(lat, lng) { return Number(lat).toFixed(4) + ', ' + Number(lng).toFixed(4); }

  // point returns the coordinates the chosen source would pin, or null.
  function point() {
    if (rExif.checked && exif) return { lat: exif.lat, lng: exif.lng, raw: true };
    if (rBrow.checked && fix) return { lat: +fix.lat, lng: +fix.lng, raw: true };
    if (rCp.checked) {
      var o = cpSel && cpSel.options[cpSel.selectedIndex];
      if (o && o.value) return { cp: o.textContent.trim(), raw: false };
    }
    return null;
  }

  // ladder pre-selects the source the server would pick if nobody chose:
  // photo GPS, else the phone, else the checkpoint. Only until the operator
  // touches the radio — after that their choice stands.
  function ladder() {
    if (touched) return;
    if (exif) rExif.checked = true;
    else if (fix) rBrow.checked = true;
    else if (cpSel && cpSel.value) rCp.checked = true;
    else rNone.checked = true;
  }

  // The milestone radios carry their published position. The one nearest the
  // pin point is flagged, never selected: marking a stop reached is deliberate.
  var msLabels = [].slice.call(document.querySelectorAll('label[data-lat][data-lng]'));
  function suggest(p) {
    var choices = msLabels.map(function (l) {
      var r = l.querySelector('input');
      return { id: r.value, lat: +l.getAttribute('data-lat'), lng: +l.getAttribute('data-lng') };
    });
    var id = p ? suggestMilestone(p, choices) : null;
    msLabels.forEach(function (l) {
      var on = id !== null && l.querySelector('input').value === id;
      l.classList.toggle('ms-suggested', on);
      var n = l.querySelector('.ms-near');
      if (n) n.hidden = !on;
    });
  }

  function refresh(fromToggle) {
    rExif.disabled = !exif;
    rBrow.disabled = !fix;
    rCp.disabled = !(cpSel && cpSel.value);
    if (rExif.disabled && rExif.checked) rExif.checked = false;
    if (rBrow.disabled && rBrow.checked) rBrow.checked = false;
    if (rCp.disabled && rCp.checked) rCp.checked = false;
    ladder();

    if (cpNote && cpSel) {
      var o = cpSel.options[cpSel.selectedIndex];
      cpNote.textContent = o && o.value ? '— ' + o.textContent.trim() : '— none picked';
    }
    // Hidden fields: the browser fix always rides along (the server uses it
    // only when it is the chosen source, or when the ladder reaches it), and
    // the photo's parsed point goes up for the record, never as the pin.
    if (la) { la.value = fix ? fix.lat : ''; ln.value = fix ? fix.lng : ''; ac.value = fix ? fix.acc : ''; }
    if (pla) { pla.value = exif ? exif.lat.toFixed(4) : ''; pln.value = exif ? exif.lng.toFixed(4) : ''; }

    var p = point(), near = p && p.raw ? nearPrivate(p.lat, p.lng) : null;
    suggest(p && p.raw ? p : null);
    fuzz.disabled = !(p && p.raw);
    if (!fromToggle) fuzz.checked = !!near && precision !== 'exact';
    fuzzSet.value = '1';              // from here on the toggle's state is deliberate

    if (!p) {
      says.textContent = rNone.checked
        ? 'publishes as: no pin — this post stays off the map'
        : 'publishes as: no pin — nothing to pin yet';
      return;
    }
    if (!p.raw) {
      says.textContent = 'publishes as: ' + p.cp + ' — the checkpoint’s own point';
      return;
    }
    if (fuzz.checked) {
      says.textContent = near
        ? 'publishes as: near ' + near.name + ' — a 5 km cell, not the point (this is a private place)'
        : 'publishes as: ' + fmt(snap(p.lat), snap(p.lng)) + ' — a 5 km cell, not the point';
      return;
    }
    says.textContent = near
      ? 'publishes as: ' + fmt(p.lat, p.lng) + ' — the exact point, and it is at a private place (' + near.name + ')'
      : 'publishes as: ' + fmt(p.lat, p.lng) + ' — the exact point, not a private place';
  }

  [rExif, rBrow, rCp, rNone].forEach(function (r) {
    r.addEventListener('change', function () { touched = true; refresh(false); });
  });
  fuzz.addEventListener('change', function () { refresh(true); });
  if (cpSel) cpSel.addEventListener('change', function () { refresh(false); });

  if (photo) {
    photo.addEventListener('change', function () {
      exif = null;
      var f = photo.files && photo.files[0];
      if (!f) {
        exSt.textContent = 'photo GPS: no file picked';
        exNote.textContent = '— no file picked';
        refresh(false);
        return;
      }
      exSt.textContent = 'photo GPS: reading…';
      var fr = new FileReader();
      fr.onerror = function () {
        exSt.textContent = 'photo GPS: could not read this file';
        exNote.textContent = '— unreadable';
        refresh(false);
      };
      fr.onload = function () {
        exif = exifGPS(fr.result);
        if (exif) {
          exSt.textContent = 'photo GPS: ' + fmt(exif.lat, exif.lng);
          exNote.textContent = '— ' + fmt(exif.lat, exif.lng);
        } else {
          exSt.textContent = 'photo GPS: none in this file';
          exNote.textContent = '— none in this file';
        }
        refresh(false);
      };
      // Slice, never the whole file: a 25 MB JPEG read into an ArrayBuffer on
      // a phone is exactly the kind of allocation that cost four posts on
      // 2026-09-02.
      fr.readAsArrayBuffer(f.slice(0, Math.min(EXIF_SLICE, f.size)));
      askFix(true);                   // the camera's position is the phone's, now
    });
  }

  // The phone's location is asked for the moment the form opens, never only
  // when a radio is picked: a camera photo carries no GPS, and the fix has to
  // be in the hidden fields before the photo is posted. It is asked again,
  // fresh, when a photo is chosen; the earlier fix stands until that arrives.
  var secure = window.isSecureContext;
  function onFix(f) {
    fix = f;
    if (geoSt) geoSt.textContent = 'location: on (±' + fix.acc + ' m)';
    refresh(false);
  }
  function onNoFix(e, fresh) {
    if (fresh && fix) {
      if (geoSt) geoSt.textContent = 'location: on (±' + fix.acc + ' m), could not refresh';
      return;
    }
    fix = null;
    if (geoSt) geoSt.textContent = 'location: off — ' + locationReason(secure, e.code);
    refresh(false);
  }
  function askFix(fresh) {
    if (geoSt) geoSt.textContent = fix ? 'location: on (±' + fix.acc + ' m), refreshing…' : 'location: asking…';
    askLocation(navigator.geolocation, onFix, function (e) { onNoFix(e, fresh); }, fresh);
  }

  // The post type decides which sections are open. The upload closes for a
  // note, and a photo already chosen is dropped with it so a hidden photo is
  // never posted. Without script every section stays open.
  var secUp = $('sec-upload'), secText = $('sec-text');
  function checkedKind() {
    var r = document.querySelector('input[name="kind"]:checked');
    return r ? r.value : '';
  }
  function applyKind() {
    var s = sectionsFor(checkedKind());
    if (secUp) secUp.hidden = !s.upload;
    if (secText) secText.open = s.textOpen;
    if (!s.upload && photo && photo.value) {
      photo.value = '';
      photo.dispatchEvent(new Event('change'));
    }
  }
  [].slice.call(document.querySelectorAll('input[name="kind"]')).forEach(function (r) {
    r.addEventListener('change', applyKind);
  });
  applyKind();

  // Start mode opens on the photo alone. "Post something else" opens the rest.
  var other = $('other-link');
  if (other) {
    other.addEventListener('click', function (ev) {
      ev.preventDefault();
      ['post-types', 'form-rest', 'start-fieldset', 'btn-post'].forEach(function (id) { var e = $(id); if (e) e.hidden = false; });
      ['btn-start', 'other-link'].forEach(function (id) { var e = $(id); if (e) e.hidden = true; });
      var h = document.querySelector('h1'); if (h) h.textContent = 'Post a Photo';
      var jp = document.querySelector('input[name="milestone_id"][value=""]'); if (jp) jp.checked = true;
      applyKind();
    });
  }

  if (geoSt && secure === false) geoSt.textContent = 'location: off — ' + locationReason(false, 1);
  else if (geoSt && navigator.geolocation) geoSt.textContent = 'location: asking…';
  else if (geoSt) geoSt.textContent = 'location: off';
  refresh(false);
  askLocation(navigator.geolocation, onFix, function (e) { onNoFix(e, false); }, false);
})();
