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
  // toggleCard opens or closes the card body under its header, and says which.
  function toggleCard(btn, body) {
    var open = !!body.hidden;
    body.hidden = !open;
    btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    return open;
  }

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
  // The same body plus the track that has just started sounding: its title, the
  // album's year and `started`, a number the page keeps for that start so a retry
  // is the same start (plays.go). Still text and numbers, never an address.
  function startedBody(album, track, idx, pos, key) {
    album = album || {};
    var enc = encodeURIComponent;
    return stateBody(album, idx, pos) + '&title=' + enc(String((track && track.title) || '')) +
      '&year=' + enc(String(album.year | 0)) + '&started=' + enc(String(Math.max(0, Math.round(key || 0))));
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
    // Only the NEXT tracks in album order, up to the limit. The playing track is
    // left to the audio element: downloading it too fought the stream for the
    // same bytes on a phone link (diagnosis 2026-11-11).
    function plan() {
      var out = [], i = cur;
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
      if (env.report) env.report('nest_cold', { code: /^http/.test(why) ? 'http' : 'cors_or_offline' });
      env.changed();
    }
    function download(i, retried) {
      var t = tracks[i];
      var status = false;
      return env.fetch(t.src).then(function (r) {
        if (!r.ok) { status = true; throw new Error('http ' + r.status); }
        return r.blob().then(function (b) {
          var ct = r.headers && r.headers.get ? r.headers.get('content-type') : '';
          return !b.type && ct ? new Blob([b], { type: ct }) : b;
        });
      }).catch(function (e) {
        // A refusal with no status (another site's storage that will not share,
        // or no signal) goes cold at once: asking for the album again cannot help.
        if (retried || !status) throw e;
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
      // playhead only moves the place; downloading ahead starts once the playing
      // track has made its first sound (started), so it never competes with it.
      playhead: function (i) { cur = i; full = false; env.changed(); },
      started: function () { pump(); },
      urlFor: function (i) { return items[i] ? items[i].url : null; },
      drop: function (i) { release(i); bad[i] = true; env.changed(); },
      ready: function () { return Object.keys(items).filter(function (k) { return Number(k) > cur; }).length; },
      held: function () { return Object.keys(items).map(Number); },
      isCold: function () { return cold; },
      config: function (o) { if (o && o.ceiling) cfg.ceiling = o.ceiling; if (o && o.ahead) cfg.ahead = o.ahead; }
    };
  }
  // THE WATCHDOG: a tap always ends in sound or a plain sentence. env:
  // setTimeout, clearTimeout, ms {stall, retry, giveUp}, onStall(phase),
  // onRetry(phase), onGiveUp(phase). arm(phase) after any start; ok() on the
  // first sound. One retry per start: after it, the next silence gives up.
  function createWatch(env) {
    var t = null, retried = false, phase = '';
    function cancel() { if (t) env.clearTimeout(t); t = null; }
    function second() {
      t = null;
      if (retried) { retried = false; env.onGiveUp(phase); return; }
      retried = true; arm(phase); env.onRetry(phase);
    }
    function stall() {
      env.onStall(phase);
      t = env.setTimeout(second, Math.max(0, (retried ? env.ms.giveUp : env.ms.retry) - env.ms.stall));
    }
    function arm(p) { cancel(); phase = p || 'stream'; t = env.setTimeout(stall, env.ms.stall); }
    return {
      arm: arm,
      ok: function () { cancel(); retried = false; },
      stop: function () { cancel(); retried = false; },
      armed: function () { return !!t; },
      phase: function () { return phase; }
    };
  }
  // A failure report's body: an event word and a few numbers. Never a place, a
  // title or an address; anything else handed in is dropped.
  function reportBody(event, f) {
    if (!/^[a-z_]{1,32}$/.test(String(event || ''))) return '';
    f = f || {};
    var b = 'event=' + event;
    var int = function (v) { return typeof v === 'number' && isFinite(v) ? Math.max(0, Math.round(v)) : null; };
    if (int(f.ms) !== null) b += '&ms=' + int(f.ms);
    if (f.code) { var c = String(f.code).toLowerCase().replace(/[^a-z0-9_]/g, '_').slice(0, 24); if (c) b += '&code=' + c; }
    if (int(f.album_id) !== null) b += '&album_id=' + int(f.album_id);
    if (int(f.track_idx) !== null) b += '&track_idx=' + int(f.track_idx);
    return b;
  }
  var api = { createWatch: createWatch, reportBody: reportBody, toggleCard: toggleCard, nextPlayable: nextPlayable, metadataFor: metadataFor, stateBody: stateBody, startedBody: startedBody, unlockThenLoad: unlockThenLoad, stripEmbed: stripEmbed, createNest: createNest, SILENT: SILENT };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;

  var root = typeof document !== 'undefined' && document.getElementById('rt-player');
  if (!root) return;
  var $ = function (id) { return document.getElementById(id); };
  var audio = $('rt-audio');
  var album = null, tracks = [], cur = 0, lastPos = 0, fails = 0, refetched = false, opening = false, waitTimer = null, startAt = 0;
  // asking: Drive Tracks has unlocked the element with the silent clip and is waiting for Chester's album.
  var asking = false;

  // Timer lengths, in ms; the node harness shortens them (RTPlayer.configTimes).
  var T = { stall: 8000, retry: 20000, giveUp: 15000, album: 25000 };
  var MSG = { stall: 'Still trying to reach the music\u2026', gaveUp: 'Could not reach the music. Tap Play to try again.',
    buffering: 'Buffering\u2026', loading: 'Still loading\u2026', nothing: 'Nothing to play yet. Ask Chester for an album.' };
  var tapAt = 0, gaveUp = false, watchFrom = 0;
  function now() { return Date.now(); }
  // Client failure reports: fire and forget, a 404 is harmless.
  function report(event, fields) {
    try {
      var b = reportBody(event, fields);
      if (!b) return;
      var p = fetch('/driver/roadtracks/event', { method: 'POST', credentials: 'same-origin', keepalive: true,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: b });
      if (p && p.catch) p.catch(function () {});
    } catch (e) { /* a report never breaks the player */ }
  }
  function setText(id, v) { var el = $(id); if (el) el.textContent = v; }
  function status(v) { setText('rt-status', v); }
  function statusText() { var el = $('rt-status'); return el ? el.textContent : ''; }
  function playBtn() { var b = $('rt-play'); if (b) b.textContent = audio.paused ? 'Play' : 'Pause'; }

  // The album, with a limit: a dead link can never leave a tap waiting for good.
  function fetchAlbum(id) {
    var ctl = typeof AbortController !== 'undefined' ? new AbortController() : null, timer = null;
    var fail = function (code) { var e = new Error(code); e.code = code; return e; };
    var limit = new Promise(function (_, rej) {
      timer = setTimeout(function () { try { if (ctl) ctl.abort(); } catch (e) { /* gone */ } rej(fail('timeout')); }, T.album);
    });
    var opts = { credentials: 'same-origin', headers: { 'Accept': 'application/json' } };
    if (ctl) opts.signal = ctl.signal;
    var got = fetch('/driver/roadtracks/album/' + encodeURIComponent(id), opts)
      .then(function (r) { if (!r.ok) throw fail('http'); return r.json(); }, function () { throw fail('network'); });
    return Promise.race([got, limit]).then(function (v) { clearTimeout(timer); return v; }, function (e) { clearTimeout(timer); throw e; });
  }
  var nest = createNest({
    fetch: function (u) { return fetch(u); },
    resign: function () { return fetchAlbum(album.album_id).then(function (doc) { album = doc; tracks = doc.tracks || []; return tracks; }); },
    makeURL: function (b) { return URL.createObjectURL(b); },
    revokeURL: function (u) { URL.revokeObjectURL(u); },
    log: function (l) { console.log(l); },
    report: function (e, f) { report(e, f); },
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
      h('pause', function () { watch.stop(); audio.pause(); });
      h('nexttrack', function () { next(); });
      h('previoustrack', function () { var p = cur - 1; while (p >= 0 && !tracks[p].src) p--; if (p >= 0) playTrack(p, 0); });
    } catch (e) { /* the lock screen is a courtesy */ }
  }

  function giveUp(msg) {
    watch.stop();
    audio.pause();
    status(msg || 'Could not play this album');
    playBtn();
  }
  // A downloaded copy plays with no network; otherwise the ordinary src does.
  // A track that starts from its beginning is one play; the same track loaded
  // again within half a minute (a retry) keeps the same start number.
  var startMark = null, pend = null;
  function markStart(i, at) {
    pend = null;
    if (at > 0 || !album) return;
    var t = now(), id = album.album_id;
    if (!(startMark && startMark.id === id && startMark.idx === i && t - startMark.at < 30000)) startMark = { id: id, idx: i, key: t, at: t, sent: false };
    if (!startMark.sent) pend = startMark;
  }
  function sendStarted(m) {
    m.sent = true;
    try {
      fetch('/driver/roadtracks/state', { method: 'POST', credentials: 'same-origin', keepalive: true,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: startedBody(album, tracks[m.idx], m.idx, audio.currentTime || 0, m.key) })
        .then(function (r) { if (!r.ok) m.sent = false; }, function () { m.sent = false; });
    } catch (e) { m.sent = false; }
  }
  function playTrack(i, at) {
    cur = i; startAt = at || 0; lastPos = startAt;
    markStart(i, at || 0);
    audio.src = nest.urlFor(i) || tracks[i].src;
    paint(); media();
    nest.playhead(i);
    gaveUp = false; watchFrom = startAt;
    watch.arm('stream');
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
    if (!refetched) { refetched = true; reconnect('Reconnecting…'); return; }
    skipOn();
  }
  // Fresh addresses for the album, then carry on from the same place.
  function reconnect(msg) {
    var at = audio.currentTime || lastPos, here = cur;
    if (msg) status(msg);
    fetchAlbum(album.album_id).then(function (doc) {
      album = doc; tracks = doc.tracks || [];
      if (tracks[here] && tracks[here].src) playTrack(here, at); else skipOn();
    }).catch(function (e) { report('album_fetch_failed', { code: e && e.code, album_id: album && album.album_id }); giveUp('Music library unavailable'); });
  }
  var watch = createWatch({
    setTimeout: function (f, ms) { return setTimeout(f, ms); }, clearTimeout: function (t) { clearTimeout(t); }, ms: T,
    onStall: function () { status(MSG.stall); report('stream_stalled', { ms: T.stall, album_id: album && album.album_id, track_idx: cur }); },
    // The album request is still running on its own limit; a stream gets fresh addresses once.
    onRetry: function (phase) { if (phase === 'stream' && album && tracks.length && !opening) reconnect(''); },
    onGiveUp: function (phase) {
      report('play_timeout', { code: phase, album_id: album && album.album_id, track_idx: cur });
      opening = false; asking = false; gaveUp = true; tapAt = 0;
      audio.pause(); status(MSG.gaveUp); playBtn();
    }
  });
  function skipOn() {
    var n = nextPlayable(tracks, cur);
    if (n < 0) giveUp('Could not play this album'); else playTrack(n, 0);
  }

  function open(id, from, at) {
    if (opening) { status(MSG.loading); return; }
    opening = true; asking = false; fails = 0; refetched = false; tapAt = now();
    watch.arm('album');
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
    }).catch(function (e) {
      opening = false; clearTimeout(waitTimer); watch.stop(); gaveUp = true;
      report('album_fetch_failed', { code: e && e.code || 'network', album_id: id });
      audio.pause(); status(e && e.code === 'timeout' ? MSG.gaveUp : 'Music library unavailable'); playBtn();
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
  audio.addEventListener('error', function () {
    if (asking) return;
    if (!opening && tracks.length) report('stream_error', { code: 'media_' + (audio.error && audio.error.code || 0), album_id: album && album.album_id, track_idx: cur });
    onError();
  });
  audio.addEventListener('timeupdate', function () {
    if (opening) return;
    var t = audio.currentTime || 0;
    // Real progress is sound: the watchdog stands down.
    if (watch.armed() && !audio.paused && t > watchFrom + 0.5) { watch.ok(); if (statusText() === MSG.buffering || statusText() === MSG.stall) status(''); }
    lastPos = t;
  });
  // A stream that stops mid-play ('waiting'; 'stalled' also fires while buffered
  // sound plays on, so it is not used): say so, and the same watchdog takes over.
  function onStalled() {
    if (opening || asking || audio.paused || !tracks.length || watch.armed()) return;
    status(MSG.buffering); watchFrom = audio.currentTime || 0; watch.arm('stream');
  }
  audio.addEventListener('waiting', onStalled);
  audio.addEventListener('loadedmetadata', function () {
    if (startAt > 0 && !opening) { try { audio.currentTime = startAt; } catch (e) { /* seek is best effort */ } startAt = 0; }
  });
  audio.addEventListener('playing', function () {
    if (opening || asking) return;
    watch.ok(); fails = 0; refetched = false; gaveUp = false; status(''); playBtn();
    if (pend && pend.idx === cur && !pend.sent) sendStarted(pend);
    if (tapAt) { report('first_sound', { ms: now() - tapAt, album_id: album && album.album_id, track_idx: cur }); tapAt = 0; }
    nest.started();
  });
  audio.addEventListener('pause', function () { if (opening || asking) return; playBtn(); sendState(); });

  // Play. Nothing loaded: it does what Resume does, or says there is nothing
  // yet; never a silent tap. Loaded: play, watched; after a give-up, from the
  // same place with a fresh load.
  var pb = $('rt-play');
  if (pb) pb.addEventListener('click', function () {
    if (opening) { status(MSG.loading); return; }
    if (!album || !tracks.length) {
      if (asking) { status('Still asking Chester\u2026'); return; }
      if (rb && parseInt(rb.getAttribute('data-album-id'), 10)) { resume(); return; }
      status(MSG.nothing); return;
    }
    if (!audio.paused) { watch.stop(); audio.pause(); return; }
    tapAt = now();
    if (gaveUp) { playTrack(cur, audio.currentTime || lastPos); return; }
    watchFrom = audio.currentTime || 0; watch.arm('stream');
    var p = audio.play();
    if (p && p.catch) p.catch(function (e) { if (!(e && e.name === 'AbortError')) status('Tap Play to start'); });
  });
  function mark(kind) {
    var t = tracks[cur];
    if (!album || !t) { status('Nothing playing to save.'); return; }
    fetch('/driver/roadtracks/mark', { method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: stateBody(album, cur, audio.currentTime) + '&kind=' + kind + '&title=' + encodeURIComponent(t.title || '') })
      .then(function (r) { status(r.ok ? (kind === 'bookmark' ? 'Marked this point. See Plan.' : 'Favourited. See Plan.') : 'Could not save that.'); })
      .catch(function () { status('Could not save that.'); });
  }
  var fb = $('rt-fav'); if (fb) fb.addEventListener('click', function () { mark('favourite'); });
  var bm = $('rt-bookmark'); if (bm) bm.addEventListener('click', function () { mark('bookmark'); });
  var nb = $('rt-next');
  if (nb) nb.addEventListener('click', function () { if (tracks.length) next(); });
  var rb = $('rt-resume');
  function resume() {
    var id = parseInt(rb.getAttribute('data-album-id'), 10);
    if (!id) return;
    open(id, { album: rb.getAttribute('data-album'), albumartist: rb.getAttribute('data-artist'), idx: parseInt(rb.getAttribute('data-idx'), 10) || 0 },
      parseFloat(rb.getAttribute('data-pos')) || 0);
    rb.hidden = true;
  }
  if (rb) rb.addEventListener('click', resume);

  // Drive Tracks. asking() is called inside the tap: it unlocks sound for the
  // page and shows a line; the album comes later, from start().
  // say() ends the wait with a plain sentence when there is no album.
  function ask(msg) {
    if (opening) return;
    asking = true; watch.stop(); status(msg || '');
    setText('rt-title', ''); setText('rt-artist', ''); setText('rt-track', '');
    audio.src = SILENT;
    try { var p = audio.play(); if (p && p.catch) p.catch(function () {}); } catch (e) { /* start() reports */ }
  }
  function say(msg) {
    if (opening) return;
    asking = false; watch.stop(); audio.pause(); status(msg); playBtn();
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
  var cardToggle = $('rt-toggle'), cardBody = $('rt-card-body');
  if (cardToggle && cardBody) cardToggle.addEventListener('click', function () { toggleCard(cardToggle, cardBody); });
  var askBtn = $('hud-drivetracks');
  if (askBtn && window.RTDriveTracks) askBtn.addEventListener('click', function () {
    if (askBtn.disabled) return;
    askBtn.disabled = true;
    var enable = function () { askBtn.disabled = false; };
    try {
      var body = window.RTDriveTracks.body(hudFix());
      window.RTDriveTracks({ fetch: window.fetch.bind(window), player: window.RTPlayer, body: body }).then(enable, enable);
    } catch (e) { enable(); }
  });
  window.RTPlayer = {
    asking: ask, say: say, stop: function () { watch.stop(); stop(); }, report: report,
    configTimes: function (o) { for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k) && T.hasOwnProperty(k)) T[k] = o[k]; },
    start: function (a) { open(a.album_id, { album: a.album, albumartist: a.albumartist, idx: 0 }, 0); },
    configNest: function (o) { nest.config(o); },
    _state: function () { return { cur: cur, held: nest.held() }; }
  };
  nest.config({});
})();
