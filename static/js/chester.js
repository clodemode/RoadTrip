/* roadtrip — Chester: ask, and watch the answer arrive.
 *
 * The form posts to /chester/ask and the answer comes back one JSON object a
 * line: text as it is written, a status while it thinks or searches, the
 * place cards, one plain error sentence, and "done". Everything shown is set
 * as text, never as markup: the answer and the cards are words a model wrote,
 * and the only addresses that become links are the ones the server built.
 *
 * On any failure the question stays in the box.
 */
(function () {
  'use strict';
  var $ = function (id) { return document.getElementById(id); };
  var form = $('ch-form'), box = $('ch-q'), send = $('ch-send'),
      status = $('ch-status'), error = $('ch-error'), answer = $('ch-answer'),
      places = $('ch-places'), meta = $('ch-meta'), speak = $('ch-speak'),
      locate = $('ch-locate'), geo = $('ch-geo'), la = $('ch-lat'), ln = $('ch-lng');
  if (!form || !box || !send || !answer) return;

  var busy = false;

  function show(el, text) {
    if (text !== undefined) el.textContent = text;
    el.hidden = false;
  }
  function hide(el) { el.hidden = true; }

  // --- read aloud: the browser's own voice, and only when asked ----------------
  var canSpeak = 'speechSynthesis' in window && typeof window.SpeechSynthesisUtterance === 'function';
  if (!canSpeak) {
    speak.disabled = true;
    speak.parentNode.appendChild(document.createTextNode(' (this browser cannot)'));
  } else {
    try { speak.checked = window.localStorage.getItem('chester-speak') === '1'; } catch (e) { /* private mode */ }
    speak.addEventListener('change', function () {
      try { window.localStorage.setItem('chester-speak', speak.checked ? '1' : '0'); } catch (e) { /* private mode */ }
      if (!speak.checked) window.speechSynthesis.cancel();
    });
  }
  function readAloud(text) {
    if (!canSpeak || !speak.checked || !text) return;
    window.speechSynthesis.cancel();
    window.speechSynthesis.speak(new window.SpeechSynthesisUtterance(text));
  }

  // --- the position: sent only when asked for, and said out loud -------------------
  if (locate) {
    locate.addEventListener('click', function () {
      if (!navigator.geolocation) { geo.textContent = 'This browser has no position to give.'; return; }
      geo.textContent = 'Asking the phone…';
      navigator.geolocation.getCurrentPosition(function (p) {
        la.value = p.coords.latitude.toFixed(4);
        ln.value = p.coords.longitude.toFixed(4);
        geo.textContent = 'Position ready. It goes with your next question only, coarsened near a private place.';
      }, function () {
        la.value = ''; ln.value = '';
        geo.textContent = 'The phone did not give a position. Chester will use the last pinned post.';
      }, { enableHighAccuracy: false, timeout: 15000, maximumAge: 60000 });
    });
  }

  // --- the cards ---------------------------------------------------------------------
  function card(p) {
    var li = document.createElement('li');
    li.className = 'ch-place';
    var kind = document.createElement('span');
    kind.className = 'ch-kind';
    kind.textContent = p.kind_label || 'Place';
    var name = document.createElement('b');
    name.className = 'ch-place-name';
    name.textContent = p.name || '';
    li.appendChild(kind);
    li.appendChild(name);
    if (p.town) {
      var town = document.createElement('span');
      town.className = 'muted';
      town.textContent = p.town;
      li.appendChild(town);
    }
    if (p.why) {
      var why = document.createElement('p');
      why.className = 'ch-why';
      why.textContent = p.why;
      li.appendChild(why);
    }
    var links = document.createElement('p');
    links.className = 'ch-links';
    (p.links || []).forEach(function (l) {
      // Only an https or http address the server built becomes a link.
      if (!/^https?:\/\//i.test(l.href || '')) return;
      var a = document.createElement('a');
      a.className = 'ch-link';
      a.href = l.href;
      a.target = '_blank';
      a.rel = 'noopener noreferrer nofollow';
      a.textContent = l.label || 'Open';
      links.appendChild(a);
    });
    li.appendChild(links);
    return li;
  }

  // --- one event ----------------------------------------------------------------------
  var spoken = '';
  function handle(ev) {
    switch (ev.t) {
      case 'status':
        if (answer.hidden) show(status, ev.d + '…');
        break;
      case 'text':
        hide(status);
        answer.hidden = false;
        answer.appendChild(document.createTextNode(ev.d));
        spoken += ev.d;
        break;
      case 'places':
        places.textContent = '';
        (ev.places || []).forEach(function (p) { places.appendChild(card(p)); });
        places.hidden = !(ev.places || []).length;
        break;
      case 'error':
        hide(status);
        show(error, ev.d);
        break;
      case 'done':
        hide(status);
        var bits = [];
        if (ev.ms) bits.push((ev.ms / 1000).toFixed(1) + ' seconds');
        if (ev.usd) bits.push('$' + ev.usd.toFixed(4));
        if (ev.id) bits.push('logged as question ' + ev.id);
        if (bits.length) show(meta, bits.join(' · '));
        if (error.hidden) readAloud(spoken);
        break;
    }
  }

  function fail(text) {
    hide(status);
    show(error, text);
  }

  // --- asking ----------------------------------------------------------------------------
  function ask(preset) {
    if (busy) return;
    var body = new URLSearchParams();
    var mode = form.querySelector('input[name="mode"]:checked');
    body.set('mode', mode ? mode.value : 'quick');
    if (preset) {
      body.set('preset', preset);
      body.set('mode', 'quick');
    } else {
      if (!box.value.trim()) { fail('Ask a question first.'); box.focus(); return; }
      body.set('q', box.value);
    }
    if (la.value && ln.value) {
      // A position goes with one question and is then forgotten: the car moves,
      // and an old point sent as "just now" would be a quiet lie.
      body.set('lat', la.value); body.set('lng', ln.value);
      la.value = ''; ln.value = '';
      if (geo) geo.textContent = 'Sent with that question. Tap again before the next one to send a fresh position.';
    }

    busy = true;
    send.disabled = true;
    spoken = '';
    answer.textContent = '';
    places.textContent = '';
    hide(answer); hide(places); hide(error); hide(meta);
    show(status, 'Asking…');
    if (canSpeak) window.speechSynthesis.cancel();

    var finished = false;
    fetch(form.action, {
      method: 'POST', body: body, credentials: 'same-origin',
      headers: { 'Accept': 'application/x-ndjson' }
    }).then(function (res) {
      var type = res.headers.get('Content-Type') || '';
      if (type.indexOf('application/x-ndjson') !== 0) {
        // Refused before it was sent: a status and one sentence.
        return res.json().then(function (j) {
          fail((j && j.error) || 'Chester could not take the question.');
        }, function () {
          fail(res.status === 401 || res.redirected ? 'You are signed out. Sign in and ask again.'
                                                    : 'Chester could not take the question.');
        });
      }
      var reader = res.body.getReader(), dec = new TextDecoder(), buf = '';
      function pump() {
        return reader.read().then(function (step) {
          buf += dec.decode(step.value || new Uint8Array(0), { stream: !step.done });
          var lines = buf.split('\n');
          buf = lines.pop();
          lines.forEach(function (line) {
            if (!line.trim()) return;
            var ev;
            try { ev = JSON.parse(line); } catch (e) { return; }
            if (ev.t === 'done') finished = true;
            handle(ev);
          });
          if (!step.done) return pump();
          if (!finished) fail('The connection dropped before the answer finished. The question is still in the box.');
        });
      }
      return pump();
    }).catch(function () {
      fail('The connection dropped. The question is still in the box.');
    }).then(function () {
      busy = false;
      send.disabled = false;
    });
  }

  form.addEventListener('submit', function (e) { e.preventDefault(); ask(''); });
  Array.prototype.forEach.call(form.querySelectorAll('.ch-preset'), function (b) {
    b.addEventListener('click', function () {
      box.value = b.getAttribute('data-question') || '';
      ask(b.getAttribute('data-preset'));
    });
  });
})();
