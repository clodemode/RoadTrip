/* roadtrip — My places (profile.go). Set Home asks the phone where it is and
 * puts that point, with its accuracy, into the form; the server refuses a fix
 * worse than the page's limit. The button answers at once. */
(function () {
  'use strict';
  var btn = document.getElementById('set-home');
  if (!btn) return;
  var max = Number(btn.getAttribute('data-max')) || 100;
  var note = document.getElementById('home-point');
  function field(id, v) { document.getElementById(id).value = v; }
  btn.addEventListener('click', function () {
    if (!navigator.geolocation) { note.textContent = 'This phone gives no location.'; return; }
    btn.disabled = true;
    note.textContent = 'Asking the phone where it is…';
    navigator.geolocation.getCurrentPosition(function (pos) {
      btn.disabled = false;
      var acc = Math.round(pos.coords.accuracy || 0);
      if (!acc || acc > max) {
        note.textContent = 'The fix is ' + acc + ' m: Home needs one within ' + max + ' m. Step outside and try again.';
        return;
      }
      field('home_lat', String(pos.coords.latitude));
      field('home_lng', String(pos.coords.longitude));
      field('home_accuracy', String(acc));
      note.textContent = 'Got a fix within ' + acc + ' m. Tap Save to keep it as Home.';
    }, function () {
      btn.disabled = false;
      note.textContent = 'The phone refused its location.';
    }, { enableHighAccuracy: true, timeout: 15000, maximumAge: 0 });
  });
})();
