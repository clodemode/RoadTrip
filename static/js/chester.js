/* roadtrip — Chester: ask, and watch the answer arrive.
 *
 * The form posts to /chester/ask and the answer comes back one JSON object a
 * line: text as it is written, a status while it thinks or searches, the
 * place cards, one plain error sentence, and "done". An error that says the
 * question failed takes down whatever words had arrived: half an answer is
 * not an answer. Everything shown is set
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
      places = $('ch-places'), music = $('ch-music'), podcasts = $('ch-podcasts'), proposal = $('ch-proposal'), meta = $('ch-meta'), speak = $('ch-speak'),
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
  var simOn = form.getAttribute('data-sim') === '1';
  if (locate && simOn) {
    // A simulated drive is the phone: the server answers from its own copy of
    // the car, so the button asks nothing and no coordinate is held here.
    locate.textContent = 'Simulated position';
    locate.disabled = true;
    geo.textContent = 'Chester will use the simulated car, not the phone.';
    la.value = ''; ln.value = '';
  } else if (locate) {
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
    if (p.route) {
      // A real form, so the browser lands on the driver screen. The place goes
      // as text and the server looks it up; the plan is not touched.
      var go = document.createElement('form');
      go.method = 'post';
      go.action = '/driver/freelance/dest';
      go.className = 'ch-route';
      var dest = document.createElement('input');
      dest.type = 'hidden';
      dest.name = 'dest';
      dest.value = p.route;
      var btn = document.createElement('button');
      btn.type = 'submit';
      btn.className = 'btn btn-sm btn-outline-dark';
      btn.textContent = 'Route me here';
      go.appendChild(dest);
      go.appendChild(btn);
      li.appendChild(go);
    }
    return li;
  }

  // An album card: the words are set as text, and Play is a link the server
  // built to the album page on the music library, opened in a new tab. Nothing
  // plays by itself; the library's own page starts the music on a second tap.
  function albumCard(m) {
    var li = document.createElement('li');
    li.className = 'ch-place ch-album';
    var kind = document.createElement('span');
    kind.className = 'ch-kind';
    kind.textContent = 'Album';
    var name = document.createElement('b');
    name.className = 'ch-place-name';
    name.textContent = m.album || '';
    li.appendChild(kind);
    li.appendChild(name);
    var by = document.createElement('span');
    by.className = 'muted';
    by.textContent = (m.artist || '') + (m.year ? ' (' + m.year + ')' : '');
    li.appendChild(by);
    if (m.why) {
      var why = document.createElement('p');
      why.className = 'ch-why';
      why.textContent = m.why;
      li.appendChild(why);
    }
    if (/^https?:\/\//i.test(m.href || '')) {
      var a = document.createElement('a');
      a.className = 'ch-play';
      a.href = m.href;
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      a.textContent = m.label || 'Play';
      li.appendChild(a);
    }
    return li;
  }

  // A podcast card: the words are set as text, and the one button is a link the
  // server built to the episode in Apple Podcasts, opened in a new tab.
  function podcastCard(p) {
    var li = document.createElement('li');
    li.className = 'ch-place ch-podcast';
    var kind = document.createElement('span');
    kind.className = 'ch-kind';
    kind.textContent = 'Podcast episode';
    var name = document.createElement('b');
    name.className = 'ch-place-name';
    name.textContent = p.episode || '';
    li.appendChild(kind);
    li.appendChild(name);
    var by = document.createElement('span');
    by.className = 'muted';
    by.textContent = (p.show || '') + ', ' + (p.minutes || 0) + ' minutes' + (p.year ? ', ' + p.year : '');
    li.appendChild(by);
    if (p.why) {
      var why = document.createElement('p');
      why.className = 'ch-why';
      why.textContent = p.why;
      li.appendChild(why);
    }
    if (/^https:\/\//i.test(p.href || '')) {
      var a = document.createElement('a');
      a.className = 'ch-play ch-podcast-open';
      a.href = p.href;
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      a.textContent = p.label || 'Open in Apple Podcasts';
      li.appendChild(a);
    }
    return li;
  }

  // A route proposal: words as text only. Save posts the card's whole
  // selection to the existing plan save, so Chester changes nothing and the
  // plan's own guard decides; the answer is written into the card, in place.
  function proposalCard(p) {
    var li = document.createElement('li');
    li.className = 'ch-place ch-proposal';
    function line(cls, text, tag) {
      var el = document.createElement(tag || 'p');
      el.className = cls;
      el.textContent = text;
      li.appendChild(el);
      return el;
    }
    line('ch-kind', 'Route choice', 'span');
    line('ch-place-name', (p.option_title || p.option) + ': ' + (p.choice_label || p.choice), 'b');
    if (p.detail) line('muted', p.detail, 'span');
    line('ch-why', 'Changes the whole trip by ' + p.miles_delta + ' miles and ' + p.hours_delta +
      ' hours (plan estimate, not traffic).');
    if (p.why) line('ch-why', p.why);
    if (p.in_progress) {
      line('ch-why error', 'This is the leg being driven now. Saving changes the road under the car.');
    }
    var result = line('muted small', '');
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn btn-sm btn-outline-dark';
    btn.textContent = 'Save';
    btn.addEventListener('click', function () {
      btn.disabled = true;
      // The plan save is strict: it is sent every choice, not only the change.
      var body = new URLSearchParams();
      Object.keys(p.selection || {}).forEach(function (k) { body.set('opt-' + k, p.selection[k]); });
      body.set('start', p.start || '');
      if (p.query_id) body.set('chester_id', String(p.query_id));
      fetch('/driver/plan', {
        method: 'POST', body: body, credentials: 'same-origin',
        headers: { 'Accept': 'application/json' }
      }).then(function (res) {
        return res.json().then(function (j) {
          result.textContent = j && j.status === 'ok' ? 'Saved.' : 'Not saved: ' + ((j && j.error) || 'the plan was not changed.');
        });
      }).catch(function () {
        result.textContent = 'Not saved: the connection dropped. The plan was not changed.';
      });
    });
    li.appendChild(btn);
    li.appendChild(result);
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
      case 'music':
        music.textContent = '';
        (ev.music || []).forEach(function (m) { music.appendChild(albumCard(m)); });
        music.hidden = !(ev.music || []).length;
        break;
      case 'podcasts':
        podcasts.textContent = '';
        (ev.podcasts || []).forEach(function (p) { podcasts.appendChild(podcastCard(p)); });
        podcasts.hidden = !(ev.podcasts || []).length;
        break;
      case 'proposal':
        proposal.textContent = '';
        if (ev.proposal) proposal.appendChild(proposalCard(ev.proposal));
        proposal.hidden = !ev.proposal;
        break;
      case 'error':
        hide(status);
        if (ev.drop) {
          // The question failed. What was streamed so far is not an answer:
          // take it down, and let the sentence stand in its place.
          answer.textContent = '';
          places.textContent = '';
          music.textContent = '';
          podcasts.textContent = '';
          proposal.textContent = '';
          hide(answer); hide(places); hide(music); hide(podcasts); hide(proposal);
          spoken = '';
        }
        show(error, ev.d);
        break;
      case 'done':
        hide(status);
        var bits = [];
        if (ev.ms) bits.push((ev.ms / 1000).toFixed(1) + ' seconds');
        if (ev.usd) bits.push('$' + ev.usd.toFixed(4) + (ev.est ? ' (an estimate)' : ''));
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
    music.textContent = '';
    proposal.textContent = '';
    hide(answer); hide(places); hide(music); hide(proposal); hide(error); hide(meta);
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
