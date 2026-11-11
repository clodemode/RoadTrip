// Runs frame.js (the script every embedded page carries) against one link or
// one form and prints what it did. Read by continuous_node_test.go.
//   echo '{"kind":"link","href":"/plan"}' | node static/js/frame_harness.mjs
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const input = JSON.parse(readFileSync(0, 'utf8'));
const out = { stopped: 0, replaced: [], reports: [] };
const listeners = {};
const origin = 'https://roadtrip.example.test';
const mk = (attrs, tag) => ({ tagName: tag, attrs: Object.assign({}, attrs), children: [],
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }, setAttribute(k, v) { this.attrs[k] = String(v); },
  removeAttribute(k) { delete this.attrs[k]; },
  appendChild(c) { this.children.push(c); }, querySelector(sel) { const m = /name="?(\w+)"?/.exec(sel); return m ? this.children.find((c) => c.attrs.name === m[1]) || null : null; },
  closest(sel) { return (sel.startsWith('a') && this.tagName === 'A') || (sel === 'form' && this.tagName === 'FORM') ? this : null; } });
const doc = { addEventListener(t, f) { (listeners[t] = listeners[t] || []).push(f); }, createElement: (t) => mk({}, t.toUpperCase()), readyState: 'complete', body: mk({}, 'BODY'), querySelectorAll: () => [] };
// input.top: the page is the top window (no frame); input.page: its address; input.login: the sign-in page.
const page = new URL(input.page || '/plan?embed=1', origin);
const win = { location: { origin, href: page.href, pathname: page.pathname, search: page.search, hash: page.hash, replace(u) { out.replaced.push(u); } }, addEventListener() {} };
if (input.login) doc.body.attrs['data-page'] = 'login';
const top = { RTPlayer: { stop() { out.stopped++; } } };
const fetch = (url, opts) => { out.reports.push({ url, body: opts.body, keepalive: opts.keepalive === true }); return Promise.resolve({ ok: false, status: 404 }); };
const ctx = { document: doc, window: win, URL, URLSearchParams, module: { exports: {} }, Object, String, console, fetch };
ctx.window.top = input.top ? win : top; ctx.top = ctx.window.top; ctx.location = win.location;
win.document = doc;
vm.createContext(ctx);
vm.runInContext(readFileSync(new URL('./frame.js', import.meta.url), 'utf8'), ctx);
const fire = (type, target) => { const e = { type, target, preventDefault() {} }; for (const f of listeners[type] || []) f(e); };
if (input.kind === 'link') {
  const a = mk(Object.assign({ href: input.href }, input.target ? { target: input.target } : {}), 'A');
  fire('click', a);
  out.href = a.attrs.href; out.target = a.attrs.target || ''; out.rel = a.attrs.rel || '';
} else if (input.kind === 'load') {
  // nothing to fire: the load-time check already ran
} else {
  const f = mk({ method: input.method, action: input.action }, 'FORM');
  if (input.target) f.attrs.target = input.target;
  fire('submit', f);
  out.action = f.attrs.action; out.target = f.attrs.target || '';
  out.embedInput = f.children.some((c) => c.attrs.name === 'embed' && c.attrs.value === '1');
}
console.log(JSON.stringify(out));
