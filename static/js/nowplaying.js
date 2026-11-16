/* roadtrip — what is playing, for everyone.
 *
 * The pure parts of the "Currently playing" line and the Listen Inn list
 * (listeninn.js draws the card; map.js draws a note for each play with a point). /tracks.json is text only: a track, an album, an
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

  // The time in the zone where the song played (the site's rule: times are shown
  // in the zone where they happened), with the zone named, e.g. "Fri, 5:23 PM CST".
  // A play with no zone, or one the browser does not know, is shown in the
  // viewer's own zone; a time that will not parse is shown as nothing.
  function fmtWhen(iso, tz) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    var o = { weekday: 'short', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' };
    if (tz) {
      try { o.timeZone = tz; return d.toLocaleString([], o); } catch (e) { delete o.timeZone; }
    }
    try { return d.toLocaleString([], o); } catch (e) { return d.toISOString(); }
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

  // A song's own address: the Ride page with the card open on it.
  function songHref(id) { return '/listeninn/song/' + encodeURIComponent(id); }

  // One row, used by the list and by a marker's popup so they read the same.
  // esc is the page's own HTML escaper.
  // listen: the doc says visitors may hear it, so a row with a name gets a Play button.
  // grouped: the row sits under its album's heading, which already says the album,
  // the artist and the blurb, so the row says only the time and the track.
  // link: a row with a name carries a link to its own address, to copy or share.
  function rowHtml(p, esc, listen, grouped, link) {
    var year = p.year ? ' (' + p.year + ')' : '';
    var can = listen && p.id;
    return '<div class="np-row"' + (p.id ? ' data-id="' + esc(p.id) + '"' : '') + '>' +
      (can ? '<button type="button" class="np-play" data-id="' + esc(p.id) + '" aria-label="Play ' + esc(p.track) + '">Play</button>' : '') +
      (link && p.id ? '<a class="np-link" href="' + esc(songHref(p.id)) + '" aria-label="Link to ' + esc(p.track) + '">Copy link</a>' : '') +
      '<p class="np-when">' + esc(fmtWhen(p.started_at, p.tz)) + '</p>' +
      '<p class="np-track"><b>' + esc(p.track) + '</b></p>' +
      (grouped ? '' : '<p class="np-album">' + esc(p.album) + year + ' &middot; ' + esc(p.artist) + '</p>' +
        (p.note ? '<p class="np-note">' + esc(trimBlurb(p.note)) + '</p>' : '')) + '</div>';
  }

  // The Listen Inn list. /tracks.json sends the newest first; the card shows
  // time flowing downward, so the oldest song is at the top and the newest at
  // the bottom. Tracks of one album that follow each other sit under one
  // heading that carries the album, the artist and the album's blurb once,
  // instead of repeating the blurb on every track.
  function panelHtml(tracks, esc, listen, link) {
    var groups = [];
    (tracks || []).slice().reverse().forEach(function (p) {
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
        g.rows.map(function (p) { return rowHtml(p, esc, listen, true, link); }).join('') + '</section>';
    }).join('');
  }

  var api = { chipText: chipText, shouldPoll: shouldPoll, fmtWhen: fmtWhen, rowHtml: rowHtml, panelHtml: panelHtml, songHref: songHref, trimBlurb: trimBlurb, BLURB_MAX: BLURB_MAX, EVERY_MS: EVERY_MS };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.RTNowPlaying = api;
})();
