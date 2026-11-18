/* roadtrip — Drop Pin (drop_pin.go). One button in the header for anyone
 * signed in: one tap asks the phone where it is and stores that as a dot on
 * the map. Never a post, never a check-in. The button says what happened. */
(function () {
  'use strict';
  var btn = document.getElementById('drop-pin');
  if (!btn) return;
  var label = btn.innerHTML;
  function say(text, ok) {
    btn.textContent = text;
    btn.classList.toggle('btn-success', !!ok);
    btn.disabled = false;
    setTimeout(function () { btn.innerHTML = label; btn.classList.remove('btn-success'); }, 3000);
  }
  btn.addEventListener('click', function () {
    if (!navigator.geolocation) { say('No location on this phone'); return; }
    btn.disabled = true;
    btn.textContent = 'Pinning…';
    navigator.geolocation.getCurrentPosition(function (pos) {
      var body = new URLSearchParams({
        lat: String(pos.coords.latitude), lng: String(pos.coords.longitude),
        accuracy: String(pos.coords.accuracy || '')
      });
      fetch('/driver/pin', { method: 'POST', body: body, credentials: 'same-origin' })
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (d) { return [r.ok, d]; }); })
        .then(function (res) { say(res[0] ? 'Pinned ✓' : (res[1].error || 'Not pinned'), res[0]); })
        .catch(function () { say('Not pinned: no connection'); });
    }, function () { say('Not pinned: location refused'); },
    { enableHighAccuracy: true, timeout: 15000, maximumAge: 0 });
  });
})();
