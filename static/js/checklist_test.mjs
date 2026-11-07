import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// One checklist step on a stand-in page: the box, its item and its Mark done form.
function page(done) {
  const listeners = {};
  const focused = [];
  const scrolled = [];
  const textarea = { name: 'outcome', focus() { focused.push('outcome'); } };
  const details = { open: false, querySelector: (s) => (s.includes('outcome') ? textarea : null),
    scrollIntoView(o) { scrolled.push(o); } };
  const item = { classList: { toggle() {} }, querySelector: (s) => (s.includes('cl-edit') ? details : null) };
  const box = { checked: done, disabled: false, dataset: { kind: 'step', id: 's1' },
    parentNode: { querySelector: () => null, appendChild() {} },
    closest: (s) => (s.includes('cl-item') ? item : null),
    addEventListener(ev, fn) { listeners[ev] = fn; } };
  const posts = [];
  const ctx = {
    document: { querySelectorAll: () => [box], querySelector: () => null, createElement: () => ({ setAttribute() {} }) },
    fetch(url, init) { posts.push({ url, body: init.body }); return new Promise(() => {}); },
    URLSearchParams, encodeURIComponent, Array, Promise,
  };
  vm.runInNewContext(readFileSync(new URL('./checklist.js', import.meta.url), 'utf8'), ctx);
  return { box, details, posts, focused, scrolled, tap(checked) { box.checked = checked; listeners.change(); } };
}

test('ticking an open step opens its Mark done form on the notes, and saves nothing yet', () => {
  const p = page(false);
  p.tap(true);
  assert.equal(p.posts.length, 0, 'no post until Done is pressed in the form');
  assert.equal(p.details.open, true, 'the Mark done form is open');
  assert.deepEqual(p.focused, ['outcome'], 'the cursor is in What happened');
  assert.equal(p.box.checked, false, 'the box stays unticked until the step is really done');
  assert.equal(p.scrolled.length, 1, 'the form is brought into view');
});

test('unticking a done step still undoes it at once', () => {
  const p = page(true);
  p.tap(false);
  assert.equal(p.posts.length, 1);
  assert.match(p.posts[0].url, /\/driver\/checklist\/step\/s1$/);
  assert.match(p.posts[0].body, /clear=1/);
  assert.equal(p.details.open, false);
});
