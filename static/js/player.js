/* roadtrip — the RoadTracks player, in the player shell.
 *
 * While the ride is Active a signed-in user's page is a shell holding this
 * card, ONE audio element and an iframe with the real page, so music plays on
 * between screens (Kris, 2026-11-09). It stops only when the album ends, the
 * listener presses Pause, or they sign out. The frame calls window.RTPlayer
 * (same origin) and the tap that starts an album unlocks sound synchronously.
 * No audio passes through RoadTrip: the audio element fetches each src straight
 * from the library's storage. NESTING (after MusicOrgan's Cloud Player): whole
 * tracks are fetched ahead into in-memory Blobs, one download at a time, so a
 * dead zone on the road does not stop the music. RAM only: no disk, no
 * database, no cache, no save. If a download fails, nesting stays cold and the
 * ordinary src plays, quietly. The resume point is kept by the server, one per
 * signed-in user (POST /driver/roadtracks/state). Everything shown is text.
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
  // The frame's address without the frame's own flag: what the address bar shows.
  function stripEmbed(path) {
    return String(path || '').replace(/([?&])embed=1(&|$)/, function (m, a, b) { return b ? a : ''; }).replace(/\?$/, '');
  }

  // NESTING. env: fetch(url) -> Response promise, resign() -> a fresh tracks
  // array promise, makeURL(blob), revokeURL(url), log(line), changed().
  function createNest(env) {
    var cfg = { ahead: 3, ceiling: 300 * 1024 * 1024 };
    var items = {}, bad = {}, total = 0, busy = false, cold = false, full = false, cur = 0, gen = 0, tracks = [];
    function distance(i) { return Math.abs(i - cur); }
    // The playing track first, then the next ones in album order, up to the limit.
    function plan() {
      var out = [], i = cur;
      if (tracks[cur] && tracks[cur].src) out.push(cur);
      for (var n = 0; n < cfg.ahead; n++) {
        i = nextPlayable(tracks, i);
        if (i < 0) break;
        out.push(i);
      }
      return out;
    }
    function release(i) {
      var it = items[i];
      if (!it) return;
      delete items[i]; total -= it.size;
      try { env.revokeURL(it.url); } catch (e) { /* gone is gone */ }
    }
    function evict() {
      var any = false;
      while (total > cfg.ceiling) {
        var far = -1, d = -1;
        Object.keys(items).forEach(function (k) {
          k = Number(k);
          if (k === cur) return;
          var kd = distance(k);
          if (kd > d || (kd === d && k < cur)) { far = k; d = kd; }
        });
        if (far < 0) break;
        release(far); any = true;
      }
      if (any) full = true;
    }
    function goCold(why) {
      if (cold) return;
      cold = true;
      env.log('roadtracks nest: cold (' + why + '); playing from the ordinary address');
      env.changed();
    }
    function download(i, retried) {
      var t = tracks[i];
      return env.fetch(t.src).then(function (r) {
        if (!r.ok) throw new Error('http ' + r.status);
        return r.blob().then(function (b) {
          var ct = r.headers && r.headers.get ? r.headers.get('content-type') : '';
          return !b.type && ct ? new Blob([b], { type: ct }) : b;
        });
      }).catch(function (e) {
        if (retried) throw e;
        // The address may have expired: ask for the album again, quietly, and try once more.
        return env.resign().then(function (ts) {
          tracks = ts || tracks;
          if (!tracks[i] || !tracks[i].src) throw new Error('no address');
          return download(i, true);
        });
      });
    }
    function pump() {
      if (busy || cold || full) return;
      var want = plan(), i = -1;
      for (var k = 0; k < want.length; k++) if (!items[want[k]] && !bad[want[k]]) { i = want[k]; break; }
      if (i < 0) return;
      if (total >= cfg.ceiling) { full = true; return; }
      busy = true;
      var g = gen;
      download(i, false).then(function (blob) {
        if (g !== gen) return;
        busy = false;
        items[i] = { blob: blob, url: env.makeURL(blob), size: blob.size || 0 };
        total += items[i].size;
        evict(); env.changed(); pump();
      }).catch(function (e) {
        if (g !== gen) return;
        busy = false; goCold(String(e && e.message || e));
      });
    }
    return {
      setAlbum: function (ts) { gen++; busy = false; cold = false; full = false; bad = {}; Object.keys(items).forEach(function (k) { release(Number(k)); }); tracks = ts || []; cur = 0; env.changed(); },
      playhead: function (i) { cur = i; full = false; pump(); env.changed(); },
      urlFor: function (i) { return items[i] ? items[i].url : null; },
      drop: function (i) { release(i); bad[i] = true; env.changed(); },
      ready: function () { return Object.keys(items).filter(function (k) { return Number(k) > cur; }).length; },
      held: function () { return Object.keys(items).map(Number); },
      isCold: function () { return cold; },
      config: function (o) { if (o && o.ceiling) cfg.ceiling = o.ceiling; if (o && o.ahead) cfg.ahead = o.ahead; }
    };
  }
  var api = { nextPlayable: nextPlayable, metadataFor: metadataFor, stateBody: stateBody, unlockThenLoad: unlockThenLoad, stripEmbed: stripEmbed, createNest: createNest, SILENT: SILENT };
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
  function playBtn() { var b = $('rt-play'); if (b) b.textContent = audio.paused ? 'Play' : 'Pause'; }

  function fetchAlbum(id) {
    return fetch('/driver/roadtracks/album/' + encodeURIComponent(id), { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error('library'); return r.json(); });
  }
  var nest = createNest({
    fetch: function (u) { return fetch(u); },
    resign: function () { return fetchAlbum(album.album_id).then(function (doc) { album = doc; tracks = doc.tracks || []; return tracks; }); },
    makeURL: function (b) { return URL.createObjectURL(b); },
    revokeURL: function (u) { URL.revokeObjectURL(u); },
    log: function (l) { console.log(l); },
    changed: function () { setText('rt-nest', nest && nest.isCold() ? 'streaming' : (nest ? nest.ready() : 0) + ' ready'); }
  });
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
  // A downloaded copy plays with no network; otherwise the ordinary src does.
  function playTrack(i, at) {
    cur = i; startAt = at || 0; lastPos = startAt;
    audio.src = nest.urlFor(i) || tracks[i].src;
    paint(); media();
    nest.playhead(i);
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
  // A media error: a bad downloaded copy is let go and the ordinary src takes
  // over; otherwise once, ask for fresh addresses and carry on from the same
  // place; after that skip along; three in a row and stop.
  function onError() {
    if (opening || !tracks.length) return;
    if (/^blob:/.test(audio.src || '')) { var at0 = lastPos; nest.drop(cur); playTrack(cur, at0); return; }
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
    status('Loading…');
    setText('rt-title', from && from.album || ''); setText('rt-artist', from && from.albumartist || ''); setText('rt-track', '');
    waitTimer = setTimeout(function () { if (opening) status('Waking the music library…'); }, 2000);
    unlockThenLoad(audio, fetchAlbum, id).then(function (doc) {
      opening = false; clearTimeout(waitTimer);
      album = doc; tracks = doc.tracks || [];
      nest.setAlbum(tracks);
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

  // --- the frame: its pages come and go, the audio is not touched ------------
  // Keep the address bar true (the frame's path, without the frame's flag) and
  // the tab title the frame page's own, so a reload lands on the same page.
  var frame = $('rt-frame');
  function frameLoaded() {
    try {
      var w = frame.contentWindow, l = w.location;
      history.replaceState(null, '', stripEmbed(l.pathname + l.search) + l.hash);
      if (w.document && w.document.title) document.title = w.document.title;
    } catch (e) { /* a page we cannot read leaves the bar as it was */ }
  }
  if (frame) frame.addEventListener('load', frameLoaded);

  audio.addEventListener('ended', function () { if (!opening && !asking) next(); });
  audio.addEventListener('error', function () { if (!asking) onError(); });
  audio.addEventListener('timeupdate', function () { if (!opening) lastPos = audio.currentTime || 0; });
  audio.addEventListener('loadedmetadata', function () {
    if (startAt > 0 && !opening) { try { audio.currentTime = startAt; } catch (e) { /* seek is best effort */ } startAt = 0; }
  });
  audio.addEventListener('playing', function () { if (opening || asking) return; fails = 0; refetched = false; status(''); playBtn(); });
  audio.addEventListener('pause', function () { if (opening || asking) return; playBtn(); sendState(); });

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
  // page and shows a line; the album comes later, from start().
  // say() ends the wait with a plain sentence when there is no album.
  function ask(msg) {
    if (opening) return;
    asking = true; status(msg || '');
    setText('rt-title', ''); setText('rt-artist', ''); setText('rt-track', '');
    audio.src = SILENT;
    try { var p = audio.play(); if (p && p.catch) p.catch(function () {}); } catch (e) { /* start() reports */ }
  }
  function say(msg) {
    if (opening) return;
    asking = false; audio.pause(); status(msg); playBtn();
  }
  // The listener signs out: the music ends here and the place is kept.
  function stop() { audio.pause(); sendState(); }
  // Ask Chester for an album. The position comes from the frame's driver
  // screen when it is the page shown (its phone fix, the one path that
  // exists); any other page sends none and the server fills in a simulated drive's.
  function hudFix() {
    try {
      var w = (frame && frame.contentWindow) || window;
      return w.RTHud && w.RTHud.fix ? w.RTHud.fix() : null;
    } catch (e) { return null; }
  }
  var askBtn = $('hud-drivetracks');
  if (askBtn && window.RTDriveTracks) askBtn.addEventListener('click', function () {
    if (askBtn.disabled) return;
    askBtn.disabled = true;
    var body = window.RTDriveTracks.body(hudFix());
    window.RTDriveTracks({ fetch: window.fetch.bind(window), player: window.RTPlayer, body: body })
      .then(function () { askBtn.disabled = false; });
  });
  window.RTPlayer = {
    asking: ask, say: say, stop: stop,
    start: function (a) { open(a.album_id, { album: a.album, albumartist: a.albumartist, idx: 0 }, 0); },
    configNest: function (o) { nest.config(o); },
    _state: function () { return { cur: cur, held: nest.held() }; }
  };
  nest.config({});
})();
