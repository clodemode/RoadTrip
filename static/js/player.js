/* roadtrip — the RoadTracks player.
 *
 * A tap on a search result plays that album inside this page (Kris,
 * 2026-11-08). No audio passes through RoadTrip: the one audio element
 * fetches each src straight from the library's storage. Music starts only
 * from a tap: the tap itself loads a silent clip and calls play() (the
 * browser's unlock), then the album is fetched and its first track takes over
 * on the SAME element. The resume point is kept by the server, one per
 * signed-in user (POST /driver/roadtracks/state); this file sends it and never
 * puts an address in it. Everything shown is set as text.
 */
(function () {
  'use strict';
  var SILENT = 'data:audio/wav;base64,UklGRrQBAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YZABAACAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA';

  // --- pure parts, exported for node ---------------------------------------
  // The next track after idx that has an address; -1 when there is none.
  function nextPlayable(tracks, idx) {
    for (var i = idx + 1; i < (tracks || []).length; i++) if (tracks[i] && tracks[i].src) return i;
    return -1;
  }
  function metadataFor(track, album) {
    track = track || {}; album = album || {};
    var m = { title: track.title || '', artist: track.artist || album.albumartist || '', album: album.album || '', artwork: [] };
    if (album.art) m.artwork.push({ src: album.art });
    return m;
  }
  // The resume body: ids, text and numbers. Never an address.
  function stateBody(album, idx, pos) {
    album = album || {};
    var enc = encodeURIComponent;
    return 'album_id=' + enc(String(album.album_id || 0)) + '&album=' + enc(String(album.album || '')) +
      '&artist=' + enc(String(album.albumartist || '')) + '&track_idx=' + enc(String(idx | 0)) +
      '&position_s=' + enc(String(Math.max(0, Math.round((pos || 0) * 10) / 10)));
  }
  // The tap's own work: load the silent clip and call play() BEFORE anything
  // asynchronous, then ask for the album. Returns the album's promise.
  function unlockThenLoad(audio, fetchAlbum, id) {
    audio.src = SILENT;
    try { var p = audio.play(); if (p && p.catch) p.catch(function () {}); } catch (e) { /* the real play() reports */ }
    return fetchAlbum(id);
  }
  var api = { nextPlayable: nextPlayable, metadataFor: metadataFor, stateBody: stateBody, unlockThenLoad: unlockThenLoad, SILENT: SILENT };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;

  var root = typeof document !== 'undefined' && document.getElementById('rt-player');
  if (!root) return;
  var $ = function (id) { return document.getElementById(id); };
  var audio = $('rt-audio');
  var album = null, tracks = [], cur = 0, lastPos = 0, fails = 0, refetched = false, opening = false, waitTimer = null, startAt = 0;
  // asking: Drive Tracks has unlocked the element with the silent clip and is waiting for Chester's album.
  var asking = false;

  function setText(id, v) { var el = $(id); if (el) el.textContent = v; }
  function status(v) { setText('rt-status', v); }
  function show() { root.hidden = false; root.removeAttribute('hidden'); }
  function playBtn() { var b = $('rt-play'); if (b) b.textContent = audio.paused ? 'Play' : 'Pause'; }

  function fetchAlbum(id) {
    return fetch('/driver/roadtracks/album/' + encodeURIComponent(id), { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error('library'); return r.json(); });
  }
  function paint() {
    setText('rt-title', album ? album.album : '');
    setText('rt-artist', album ? album.albumartist : '');
    var t = tracks[cur];
    setText('rt-track', t ? 'Track ' + (cur + 1) + ' of ' + tracks.length + ': ' + t.title : '');
    var art = $('rt-art');
    if (art) { if (album && album.art) { art.setAttribute('src', album.art); art.hidden = false; } else { art.hidden = true; } }
    playBtn();
  }
  function media() {
    if (typeof navigator === 'undefined' || !navigator.mediaSession) return;
    try {
      if (typeof MediaMetadata !== 'undefined') navigator.mediaSession.metadata = new MediaMetadata(metadataFor(tracks[cur], album));
      var h = function (name, fn) { try { navigator.mediaSession.setActionHandler(name, fn); } catch (e) { /* not supported */ } };
      h('play', function () { audio.play(); });
      h('pause', function () { audio.pause(); });
      h('nexttrack', function () { next(); });
      h('previoustrack', function () { var p = cur - 1; while (p >= 0 && !tracks[p].src) p--; if (p >= 0) playTrack(p, 0); });
    } catch (e) { /* the lock screen is a courtesy */ }
  }

  function giveUp(msg) {
    audio.pause();
    status(msg || 'Could not play this album');
    playBtn();
  }
  function playTrack(i, at) {
    cur = i; startAt = at || 0; lastPos = startAt;
    audio.src = tracks[i].src;
    paint(); media();
    var p;
    try { p = audio.play(); } catch (e) { p = null; }
    if (p && p.catch) p.catch(function (e) {
      if (e && e.name === 'NotAllowedError') { status('Tap Play to start'); playBtn(); } else if (e && e.name === 'AbortError') { /* a newer load took over */ } else onError();
    });
  }
  function next() {
    var n = nextPlayable(tracks, cur);
    if (n < 0) { status('Album finished'); audio.pause(); playBtn(); sendState(); return; }
    playTrack(n, 0);
  }
  // A media error: once, ask for fresh addresses and carry on from the same
  // place; after that skip along; three in a row and stop.
  function onError() {
    if (opening || !tracks.length) return;
    fails++;
    if (fails >= 3) { giveUp('Could not play this album'); return; }
    if (!refetched) {
      refetched = true;
      var at = lastPos, here = cur;
      status('Reconnecting…');
      fetchAlbum(album.album_id).then(function (doc) {
        album = doc; tracks = doc.tracks || [];
        if (tracks[here] && tracks[here].src) playTrack(here, at); else skipOn();
      }).catch(function () { giveUp('Music library unavailable'); });
      return;
    }
    skipOn();
  }
  function skipOn() {
    var n = nextPlayable(tracks, cur);
    if (n < 0) giveUp('Could not play this album'); else playTrack(n, 0);
  }

  function open(id, from, at) {
    if (opening) return;
    opening = true; asking = false; fails = 0; refetched = false;
    show(); status('Loading…');
    setText('rt-title', from && from.album || ''); setText('rt-artist', from && from.albumartist || ''); setText('rt-track', '');
    waitTimer = setTimeout(function () { if (opening) status('Waking the music library…'); }, 2000);
    unlockThenLoad(audio, fetchAlbum, id).then(function (doc) {
      opening = false; clearTimeout(waitTimer);
      album = doc; tracks = doc.tracks || [];
      var i = nextPlayable(tracks, (typeof from === 'object' && from && from.idx >= 0 ? from.idx : 0) - 1);
      if (i < 0) i = nextPlayable(tracks, -1);
      if (i < 0) { giveUp('Nothing in this album can be played'); return; }
      playTrack(i, i === (from && from.idx) ? at : 0);
    }).catch(function () {
      opening = false; clearTimeout(waitTimer);
      audio.pause(); status('Music library unavailable'); playBtn();
    });
  }

  // --- the server-held resume point ----------------------------------------
  function sendState() {
    if (opening || !album || !tracks.length) return;
    var body = stateBody(album, cur, audio.currentTime || lastPos);
    try {
      fetch('/driver/roadtracks/state', { method: 'POST', credentials: 'same-origin', keepalive: true,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: body }).catch(function () {});
    } catch (e) { /* the point is a courtesy */ }
  }
  function beacon() {
    if (!album || !tracks.length || !navigator.sendBeacon) return;
    try { navigator.sendBeacon('/driver/roadtracks/state', new Blob([stateBody(album, cur, audio.currentTime || lastPos)], { type: 'application/x-www-form-urlencoded' })); } catch (e) { /* ditto */ }
  }
  setInterval(function () { if (!audio.paused && !opening) sendState(); }, 15000);
  window.addEventListener('pagehide', beacon);

  // --- keep playing: a link that leaves the page opens in a new tab while music plays ---
  function marks() {
    var as = document.querySelectorAll('#hud a[href]');
    for (var i = 0; i < as.length; i++) {
      var a = as[i], h = a.getAttribute('href') || '';
      if (h === '#' || h.charAt(0) === '#') continue;
      if (!audio.paused) {
        if (!a.getAttribute('target')) { a.setAttribute('target', '_blank'); a.setAttribute('rel', 'noopener'); a.setAttribute('data-rt-blank', '1'); }
      } else if (a.getAttribute('data-rt-blank')) {
        a.removeAttribute('target'); a.removeAttribute('data-rt-blank');
      }
    }
  }
  // A link built after the music started (the picks) is caught at the tap.
  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest ? e.target.closest('#hud a[href]') : null;
    if (a) marks();
  }, true);

  audio.addEventListener('ended', function () { if (!opening && !asking) next(); });
  audio.addEventListener('error', function () { if (!asking) onError(); });
  audio.addEventListener('timeupdate', function () { if (!opening) lastPos = audio.currentTime || 0; });
  audio.addEventListener('loadedmetadata', function () {
    if (startAt > 0 && !opening) { try { audio.currentTime = startAt; } catch (e) { /* seek is best effort */ } startAt = 0; }
  });
  audio.addEventListener('playing', function () { if (opening || asking) return; fails = 0; refetched = false; status(''); playBtn(); marks(); });
  audio.addEventListener('pause', function () { if (opening || asking) return; playBtn(); marks(); sendState(); });

  var pb = $('rt-play');
  if (pb) pb.addEventListener('click', function () {
    if (audio.paused) { var p = audio.play(); if (p && p.catch) p.catch(function () { status('Tap Play to start'); }); } else audio.pause();
  });
  var nb = $('rt-next');
  if (nb) nb.addEventListener('click', function () { if (tracks.length) next(); });
  var rb = $('rt-resume');
  if (rb) rb.addEventListener('click', function () {
    var id = parseInt(rb.getAttribute('data-album-id'), 10);
    if (!id) return;
    open(id, { album: rb.getAttribute('data-album'), albumartist: rb.getAttribute('data-artist'), idx: parseInt(rb.getAttribute('data-idx'), 10) || 0 },
      parseFloat(rb.getAttribute('data-pos')) || 0);
    rb.hidden = true;
  });

  // Drive Tracks. asking() is called inside the tap: it unlocks sound for the
  // page and shows the card with a line; the album comes later, from start().
  // say() ends the wait with a plain sentence when there is no album.
  function ask(msg) {
    if (opening) return;
    asking = true; show(); status(msg || '');
    setText('rt-title', ''); setText('rt-artist', ''); setText('rt-track', '');
    audio.src = SILENT;
    try { var p = audio.play(); if (p && p.catch) p.catch(function () {}); } catch (e) { /* start() reports */ }
  }
  function say(msg) {
    if (opening) return;
    asking = false; audio.pause(); show(); status(msg); playBtn();
  }
  // Drive Tracks on a page that is not the driver screen: the same tap, the same
  // function (hud.js exports it), no position of its own (a page with no GPS
  // sends none; the server fills in a simulated drive's).
  var dtStrip = $('hud-drivetracks');
  if (dtStrip && !$('hud') && window.RTDriveTracks) dtStrip.addEventListener('click', function () {
    if (dtStrip.disabled) return;
    dtStrip.disabled = true;
    window.RTDriveTracks({ fetch: window.fetch.bind(window), player: window.RTPlayer, body: 'source=drivetracks' })
      .then(function () { dtStrip.disabled = false; });
  });
  window.RTPlayer = { asking: ask, say: say, start: function (a) { open(a.album_id, { album: a.album, albumartist: a.albumartist, idx: 0 }, 0); } };
})();
