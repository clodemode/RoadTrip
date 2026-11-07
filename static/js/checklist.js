/* roadtrip — the checklist's boxes.
 * Ticking an open step opens its Mark done form, on What happened, so the
 * notes go in before it is marked; the form's Done button saves it and the
 * page comes back to the step. Unticking a done step undoes it at once, no
 * page reload: it posts to the same mark route the forms use, asking for
 * JSON. On any failure the box goes back where it was and a plain sentence
 * says so. */
(function () {
  'use strict';
  var boxes = document.querySelectorAll('input.cl-check');
  Array.prototype.forEach.call(boxes, function (box) {
    box.addEventListener('change', function () {
      var want = box.checked;
      var item = box.closest('.cl-item');
      var form = want && item ? item.querySelector('details.cl-edit') : null;
      if (form) {
        box.checked = false;
        form.open = true;
        form.scrollIntoView({ block: 'center', behavior: 'smooth' });
        var notes = form.querySelector('textarea[name="outcome"]');
        if (notes) notes.focus();
        return;
      }
      var old = box.parentNode.querySelector('.cl-fail');
      if (old) old.remove();
      var body = new URLSearchParams();
      if (!want) body.set('clear', '1');
      box.disabled = true;
      fetch('/driver/checklist/' + box.dataset.kind + '/' + encodeURIComponent(box.dataset.id), {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString()
      }).then(function (r) {
        if (!r.ok) throw new Error('status ' + r.status);
        return r.json();
      }).then(function (j) {
        var item = box.closest('.cl-item');
        if (item) {
          item.classList.toggle('done', !!j.done);
          var st = item.querySelector('.cl-state');
          if (st) st.textContent = j.done ? 'Done' : 'To do';
        }
        var prog = document.querySelector('.cl-progress b');
        if (prog && j.summary) prog.textContent = j.summary.steps_done + ' of ' + j.summary.steps + ' done';
      }).catch(function () {
        box.checked = !want;
        var m = box.parentNode.querySelector('.cl-fail');
        if (!m) { m = document.createElement('span'); m.className = 'cl-fail error small'; m.setAttribute('role', 'alert'); box.parentNode.appendChild(m); }
        m.textContent = ' Not saved. Check the signal and tap again.';
      }).then(function () { box.disabled = false; });
    });
  });
})();
