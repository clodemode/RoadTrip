/* roadtrip — the Listen Inn card.
 *
 * The Listen Inn is a card of its own, docked at the bottom of the Ride page
 * and off the map. Its title is its header button: a tap opens or closes it in
 * place. The list inside runs oldest to newest, and the card opens on the
 * newest song, or on the one the page was asked for (/listeninn/song/<name>).
 * Each row carries a link to its own address; a tap copies it.
 *
 * /tracks.json is text only. Nothing on this page asks for it more than once a
 * minute, and nothing asks while the tab is hidden. What was read is told to
 * the map (an 'rt:tracks' event) so its music notes stay in step, and to
 * listen.js when the page has the audio element. The pure parts are tested
 * through node; the page wiring below only runs in a browser. */
(function () {
  'use strict';

  // The song the card opens on: the one asked for when it is in the list, else
  // the newest. tracks is newest first, as /tracks.json sends it.
  function focusId(tracks, asked) {
    var ids = (tracks || []).map(function (p) { return p && p.id; }).filter(function (id) { return !!id; });
    if (asked && ids.indexOf(asked) >= 0) return asked;
    return ids.length ? ids[0] : null;
  }

  var api = { focusId: focusId };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window === 'undefined') return;
  window.RTListenInn = api;

  var NP = window.RTNowPlaying;
  var card = document.getElementById('np'), toggle = document.getElementById('np-toggle'),
      body = document.getElementById('np-body'), panel = document.getElementById('np-panel');
  if (!NP || !card || !toggle || !body || !panel) return;
  var line = document.getElementById('np-line'), label = document.getElementById('np-label');
  var asked = card.getAttribute('data-song') || '';
  var doc = window.RIDE_TRACKS || { tracks: [] };
  var last = 0;

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function rowOf(id) {
    var found = null;
    Array.prototype.forEach.call(panel.querySelectorAll('.np-row[data-id]'), function (row) {
      if (row.getAttribute('data-id') === id) found = row;
    });
    return found;
  }

  // Bring the card's song into the middle of the list. Only the list scrolls:
  // the page under the card stays where the visitor left it.
  function focus() {
    var id = focusId(doc.tracks, asked);
    var row = id ? rowOf(id) : null;
    if (!row) { panel.scrollTop = panel.scrollHeight; return; }
    panel.scrollTop = Math.max(0, row.offsetTop - (panel.clientHeight - row.offsetHeight) / 2);
  }

  function draw(d, first) {
    doc = d || doc;
    var t = NP.chipText(doc);
    /* A finished ride has no "Currently playing" line but keeps its history. */
    card.hidden = !t && !doc.finished && !(doc.tracks || []).length;
    if (t && label && line) { label.textContent = t.label; line.textContent = t.line; }
    // A visitor reading the middle of the list is left there; one at the
    // bottom stays at the bottom as new songs arrive.
    var atEnd = panel.scrollHeight - panel.scrollTop - panel.clientHeight < 24, keep = panel.scrollTop;
    panel.innerHTML = NP.panelHtml(doc.tracks || [], esc, !!doc.listen && !!document.getElementById('np-audio'), true);
    var sel = asked ? rowOf(asked) : null;
    if (sel) sel.classList.add('np-sel');
    if (first) focus(); else panel.scrollTop = atEnd ? panel.scrollHeight : keep;
    if (window.RTListen && window.RTListen.update) window.RTListen.update(doc);
    try { document.dispatchEvent(new CustomEvent('rt:tracks', { detail: doc })); } catch (e) { /* the map keeps what it had */ }
  }

  function setOpen(open) {
    body.hidden = !open;
    toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open) focus();
  }
  toggle.addEventListener('click', function () { setOpen(body.hidden); });

  // A row's link: copy its address when the browser lets a page do that, and
  // say so in the link itself; otherwise the tap just follows the link, which
  // opens the same page on that song.
  panel.addEventListener('click', function (e) {
    var a = e.target && e.target.closest ? e.target.closest('.np-link') : null;
    if (!a || !navigator.clipboard || !navigator.clipboard.writeText) return;
    e.preventDefault();
    var href = a.getAttribute('href');
    navigator.clipboard.writeText(new URL(href, window.location.href).href).then(function () {
      a.textContent = 'Copied';
      setTimeout(function () { a.textContent = 'Copy link'; }, 2000);
    }).catch(function () { window.location.href = href; });
  });

  draw(doc, true);
  if (!body.hidden) focus();
  last = Date.now();
  /* Once a minute, and only while the tab is visible: a hidden tab must not
     keep the machine awake. Coming back to the tab asks at once if it is due. */
  var ask = function () {
    if (!NP.shouldPoll(document.hidden, last, Date.now())) return;
    last = Date.now();
    fetch('/tracks.json', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d) draw(d, false); })
      .catch(function () { /* the card keeps what it had */ });
  };
  setInterval(ask, NP.EVERY_MS);
  document.addEventListener('visibilitychange', ask);
})();
