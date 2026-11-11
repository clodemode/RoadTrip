/* roadtrip — the script every page inside the player shell's frame carries.
 *
 * The shell keeps the one audio element alive by keeping this page in a frame,
 * so everything that would leave the frame is dealt with here: a link to
 * another site opens in a new tab (a site that refuses framing is never blank),
 * a same-site link and form keep embed=1 for a browser that does not say
 * "iframe" itself (only when the page really is in a frame), Sign out stops the music and goes to the top window, and the
 * sign-in page breaks out of the frame. Nothing here touches the audio but the
 * explicit stop on sign out.
 */
(function () {
  'use strict';
  var embedLink = function (href, base) {
    var u = new URL(href, base);
    if (!u.searchParams.has('embed')) u.searchParams.set('embed', '1');
    return u.pathname + u.search + u.hash;
  };
  // What to do with a link: 'blank' (leaves the app), 'top' (not a web address), 'embed', or '' (leave it).
  function classify(href, target, base, origin) {
    if (!href || href.charAt(0) === '#') return '';
    if (target && target !== '_self') return '';
    var u;
    try { u = new URL(href, base); } catch (e) { return ''; }
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'top';
    return u.origin === origin ? 'embed' : 'blank';
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = { classify: classify };
  if (typeof document === 'undefined') return;

  var loc = window.location;
  // Inside the shell's frame? A frame we cannot read is still a frame.
  var inFrame;
  try { inFrame = window.top !== window; } catch (e) { inFrame = true; }
  var isLogin = !!(document.body && document.body.getAttribute && document.body.getAttribute('data-page') === 'login');
  // A page in the top window whose address still says embed=1 lost its player
  // card (an old tab, a shared link): say so, and load it again without the flag
  // so the server serves the shell and the card.
  if (!inFrame && !isLogin && /[?&]embed=1(&|#|$)/.test(loc.search || '')) {
    try {
      fetch('/driver/roadtracks/event', { method: 'POST', credentials: 'same-origin', keepalive: true,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: 'event=card_missing' }).catch(function () {});
    } catch (e) { /* a report never stops the fix */ }
    try {
      var c = new URL(loc.href); c.searchParams.delete('embed');
      loc.replace(c.pathname + c.search + c.hash);
    } catch (e) { /* stay put */ }
  }
  // The sign-in page asked for inside the frame (a session that ended): leave the frame.
  if (document.body && document.body.getAttribute && document.body.getAttribute('data-page') === 'login' && window.top !== window) {
    try {
      var u = new URL(loc.href); u.searchParams.delete('embed');
      window.top.location.replace(u.pathname + u.search);
    } catch (e) { /* stay put */ }
  }

  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest ? e.target.closest('a[href]') : null;
    if (!a) return;
    var href = a.getAttribute('href'), how = classify(href, a.getAttribute('target'), loc.href, loc.origin);
    if (how === 'blank') { a.setAttribute('target', '_blank'); a.setAttribute('rel', 'noopener noreferrer'); }
    else if (how === 'top') a.setAttribute('target', '_top');
    else if (how === 'embed' && inFrame) a.setAttribute('href', embedLink(href, loc.href));
  }, true);

  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f || !f.getAttribute) return;
    var target = f.getAttribute('target');
    var action = f.getAttribute('action') || loc.pathname + loc.search;
    var u;
    try { u = new URL(action, loc.href); } catch (x) { return; }
    if (u.origin !== loc.origin) return;
    if (u.pathname === '/driver/logout') {
      // Sign out is the one thing that ends the music, and it leaves the frame.
      try { if (window.top.RTPlayer) window.top.RTPlayer.stop(); } catch (x) { /* the top goes away anyway */ }
      if (target !== '_top') f.setAttribute('target', '_top');
      return;
    }
    if (target && target !== '_self') return;
    if (!inFrame) return;
    if ((f.getAttribute('method') || 'get').toLowerCase() === 'post') {
      f.setAttribute('action', embedLink(action, loc.href));
    } else if (!f.querySelector('input[name="embed"]')) {
      var i = document.createElement('input');
      i.setAttribute('type', 'hidden'); i.setAttribute('name', 'embed'); i.setAttribute('value', '1');
      f.appendChild(i);
    }
  }, true);
})();
