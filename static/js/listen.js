/* roadtrip — hearing what played (Kris, 2026-11-12: "Let them hear it").
 *
 * One shared audio element on the public home page. A visitor taps Play on a
 * Listen Inn row, or Listen on the chip; the element's address is
 * /tracks/<name>/audio, which the server checks and sends on. Nothing starts by
 * itself: when a track ends the next NEWER row in the list is played, so a
 * visitor who tapped once can listen along with the drive. The pure parts are
 * tested in listen_test.mjs; the page wiring below only runs in a browser. */
(function () {
  'use strict';

  // The row that comes after this one in time. The list is newest first, so the
  // next newer row is the one before it; null for the newest row or an unknown name.
  function nextId(tracks, id) {
    var i = -1;
    (tracks || []).forEach(function (p, k) { if (p && p.id === id) i = k; });
    return i > 0 && tracks[i - 1].id ? tracks[i - 1].id : null;
  }

  // What the chip's Listen button plays: what is current, else the newest row.
  function currentId(doc) {
    var p = doc && (doc.now || doc.last);
    return p && p.id ? p.id : null;
  }

  // One plain sentence for an answer the route gave (0 = the browser could not say).
  function errorText(status) {
    if (status === 404) return 'That track is not available to hear any more.';
    if (status === 429) return 'Lots of listening just now. Try again in a minute.';
    if (status === 502 || status === 503 || status === 504) return 'The music library is not answering right now.';
    return 'That track would not play. Try it again in a moment.';
  }

  var api = { nextId: nextId, currentId: currentId, errorText: errorText };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window === 'undefined') return;
  window.RTListen = api;

  var audio = document.getElementById('np-audio');
  var panel = document.getElementById('np-panel');
  var chipBtn = document.getElementById('np-listen');
  var msg = document.getElementById('np-msg');
  if (!audio || !panel) return;

  var doc = window.RIDE_TRACKS || { tracks: [] };
  var cur = null;

  function say(text) { if (msg) { msg.textContent = text || ''; msg.hidden = !text; } }

  // Mark the sounding row and make every button say what a tap will do.
  function mark() {
    var playing = !!cur && !audio.paused && !audio.ended;
    Array.prototype.forEach.call(panel.querySelectorAll('.np-row[data-id]'), function (row) {
      var on = row.getAttribute('data-id') === cur;
      row.classList.toggle('np-on', on);
      var b = row.querySelector('.np-play');
      if (b) b.textContent = on && playing ? 'Pause' : 'Play';
    });
    if (chipBtn) chipBtn.textContent = playing ? 'Pause' : 'Listen';
  }

  function start(id) {
    if (!id) return;
    say('');
    cur = id;
    audio.src = '/tracks/' + encodeURIComponent(id) + '/audio';
    var pr = audio.play();
    if (pr && pr.catch) pr.catch(function () { /* the error event says why */ });
    mark();
  }

  function tap(id) {
    if (id === cur && audio.src && !audio.error) {
      if (audio.paused) { var pr = audio.play(); if (pr && pr.catch) pr.catch(function () {}); } else audio.pause();
      mark();
      return;
    }
    start(id);
  }

  // The element cannot say why it failed, so ask the route once more without
  // following its redirect: the status is the reason.
  audio.addEventListener('error', function () {
    if (!cur || !audio.src) return;
    var src = audio.src;
    fetch(src, { redirect: 'manual', cache: 'no-store' })
      .then(function (r) { say(errorText(r.type === 'opaqueredirect' ? 0 : r.status)); })
      .catch(function () { say(errorText(0)); });
    cur = null;
    mark();
  });
  audio.addEventListener('ended', function () {
    var n = nextId(doc.tracks, cur);
    if (n) start(n); else { cur = null; mark(); }
  });
  audio.addEventListener('play', mark);
  audio.addEventListener('pause', mark);

  panel.addEventListener('click', function (e) {
    var b = e.target && e.target.closest ? e.target.closest('.np-play') : null;
    if (b) tap(b.getAttribute('data-id'));
  });
  if (chipBtn) chipBtn.addEventListener('click', function () { tap(cur || currentId(doc)); });

  // map.js redraws the list every minute; it calls this after each redraw.
  window.RTListen.update = function (d) {
    doc = d || doc;
    if (chipBtn) chipBtn.hidden = !(doc.listen && currentId(doc));
    mark();
  };
  window.RTListen.update(doc);
})();
