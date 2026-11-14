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

  // A blurb is shown whole when it is short; a long one ends on a whole word with
  // an ellipsis, never in the middle of one. A word longer than the room is the
  // only thing that is cut inside itself.
  var BLURB_MAX = 140;
  function trimBlurb(text, max) {
    var s = String(text == null ? '' : text).replace(/\s+/g, ' ').trim();
    var limit = max || BLURB_MAX;
    if (s.length <= limit) return s;
    var cut = s.lastIndexOf(' ', limit);
    if (cut < limit / 2) cut = limit;
    return s.slice(0, cut).replace(/[\s,;:.\u2013\u2014-]+$/, '') + '\u2026';
  }

  // One row, used by the list and by a marker's popup so they read the same.
  // esc is the page's own HTML escaper.
  // listen: the doc says visitors may hear it, so a row with a name gets a Play button.
  // grouped: the row sits under its album's heading, which already says the album,
  // the artist and the blurb, so the row says only the time and the track.
  function rowHtml(p, esc, listen, grouped) {
    var year = p.year ? ' (' + p.year + ')' : '';
    var can = listen && p.id;
    return '<div class="np-row"' + (can ? ' data-id="' + esc(p.id) + '"' : '') + '>' +
      (can ? '<button type="button" class="np-play" data-id="' + esc(p.id) + '" aria-label="Play ' + esc(p.track) + '">Play</button>' : '') +
      '<p class="np-when">' + esc(fmtWhen(p.started_at)) + '</p>' +
      '<p class="np-track"><b>' + esc(p.track) + '</b></p>' +
      (grouped ? '' : '<p class="np-album">' + esc(p.album) + year + ' &middot; ' + esc(p.artist) + '</p>' +
        (p.note ? '<p class="np-note">' + esc(trimBlurb(p.note)) + '</p>' : '')) + '</div>';
  }

  // The Listen Inn list, newest first as /tracks.json sends it. Tracks of one
  // album that follow each other sit under one heading that carries the album,
  // the artist and the album's blurb once, instead of repeating the blurb on
  // every track.
  function panelHtml(tracks, esc, listen) {
    var groups = [];
    (tracks || []).forEach(function (p) {
      var g = groups[groups.length - 1];
      if (g && g.album === p.album && g.artist === p.artist) g.rows.push(p);
      else groups.push({ album: p.album, artist: p.artist, year: p.year, rows: [p] });
    });
    return groups.map(function (g) {
      var note = '';
      g.rows.forEach(function (p) { if (!note && p.note) note = p.note; });
      var year = g.year ? ' (' + g.year + ')' : '';
      return '<section class="np-group"><p class="np-album np-album-h">' + esc(g.album) + year + ' &middot; ' + esc(g.artist) + '</p>' +
        (note ? '<p class="np-note">' + esc(trimBlurb(note)) + '</p>' : '') +
        g.rows.map(function (p) { return rowHtml(p, esc, listen, true); }).join('') + '</section>';
    }).join('');
  }

  var api = { chipText: chipText, shouldPoll: shouldPoll, fmtWhen: fmtWhen, rowHtml: rowHtml, panelHtml: panelHtml, trimBlurb: trimBlurb, BLURB_MAX: BLURB_MAX, EVERY_MS: EVERY_MS };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.RTNowPlaying = api;
})();
