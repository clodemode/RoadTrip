/* roadtrip — what is playing, for everyone.
 *
 * The pure parts of the "Currently playing" chip and the Listen Inn list on the
 * map (map.js draws them). /tracks.json is text only: a track, an album, an
 * artist, a time and a zone, Chester's note and, when there is one, a published
 * point. There is no audio and no address here. Nothing on this page asks for
 * more than once a minute, and nothing asks while the tab is hidden. */
(function () {
  'use strict';

  var EVERY_MS = 60000;

  // What the chip says: the label and one line, or null when there is nothing.
  function chipText(doc) {
    var p = doc && (doc.now || doc.last);
    if (!p) return null;
    var bits = [p.track, p.artist, p.album].filter(function (s) { return s; });
    return { label: doc.now ? 'Currently playing' : 'Last played', line: bits.join(' · '), fresh: !!doc.now };
  }

  // Ask again only while the tab is visible and a minute has gone by.
  function shouldPoll(hidden, lastMs, nowMs, everyMs) {
    if (hidden) return false;
    return !lastMs || nowMs - lastMs >= (everyMs || EVERY_MS) - 1000;
  }

  // The viewer's own time with the zone shown, e.g. "Thu 2:05 PM EST". Falls back
  // to the ISO text if the browser cannot format it, and to nothing if it will not parse.
  function fmtWhen(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    try {
      return d.toLocaleString([], { weekday: 'short', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' });
    } catch (e) { return d.toISOString(); }
  }

  // One row, used by the list and by a marker's popup so they read the same.
  // esc is the page's own HTML escaper.
  function rowHtml(p, esc) {
    var year = p.year ? ' (' + p.year + ')' : '';
    return '<div class="np-row"><p class="np-when">' + esc(fmtWhen(p.started_at)) + '</p>' +
      '<p class="np-track"><b>' + esc(p.track) + '</b></p>' +
      '<p class="np-album">' + esc(p.album) + year + ' &middot; ' + esc(p.artist) + '</p>' +
      (p.note ? '<p class="np-note">' + esc(p.note) + '</p>' : '') + '</div>';
  }

  var api = { chipText: chipText, shouldPoll: shouldPoll, fmtWhen: fmtWhen, rowHtml: rowHtml, EVERY_MS: EVERY_MS };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.RTNowPlaying = api;
})();
