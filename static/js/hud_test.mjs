import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// A permissive stand-in for the page: every element exists and takes any write.
function fakeEl() {
  const el = { textContent: '', value: '', disabled: false, style: {}, className: '', hidden: false,
    setAttribute() {}, getAttribute(n) { return n === 'data-url' ? '/driver/hud.json' : ''; },
    appendChild() {}, addEventListener() {}, removeAttribute() {}, querySelector() { return fakeEl(); },
    classList: { add() {}, remove() {}, toggle() {} } };
  return el;
}

function loadHud() {
  const calls = [];            // every fetch URL, with its resolver
  let geoOk = null;
  const ctx = {
    document: { getElementById: () => fakeEl(), createElement: () => fakeEl(), hidden: false, addEventListener() {} },
    navigator: { onLine: true, geolocation: { watchPosition(ok) { geoOk = ok; return 1; } } },
    window: { addEventListener() {} },
    setInterval() { return 1; }, clearInterval() {}, setTimeout(f) { return 1; },
    fetch(url) {
      return new Promise((res) => calls.push({ url, done: () => res({ ok: true, json: async () => ({ hazards: [] }) }) }));
    },
    encodeURIComponent, Date, Math, JSON, Promise, console,
  };
  ctx.window.document = ctx.document;
  vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), ctx);
  return { calls, fix: (lat, lng) => geoOk({ coords: { latitude: lat, longitude: lng, accuracy: 10 } }) };
}
const settle = () => new Promise((r) => setImmediate(r));

test('a first fix that lands while the load request is in flight is asked for as soon as it returns', async () => {
  const h = loadHud();
  assert.equal(h.calls.length, 1, 'one request on load');
  assert.ok(!h.calls[0].url.includes('lat='), 'the load request carries no position');
  h.fix(30.2672, -97.7431);    // the fix arrives while that request is still busy
  h.calls[0].done();
  await settle(); await settle();
  assert.equal(h.calls.length, 2, 'the dropped first-fix ask is made once the load request returns');
  assert.ok(h.calls[1].url.includes('lat=30.2672'), 'and it carries the position');
});

test('a fix with nothing in flight asks at once, and no extra request follows', async () => {
  const h = loadHud();
  h.calls[0].done();
  await settle();
  h.fix(30.2672, -97.7431);
  assert.equal(h.calls.length, 2);
  h.calls[1].done();
  await settle(); await settle();
  assert.equal(h.calls.length, 2);
});

test('telemetryBody builds the body and refuses any coordinate key at any depth', () => {
  const mod = { exports: {} };
  const ctx = { module: mod, document: { getElementById: () => null, addEventListener() {} }, window: { addEventListener() {} }, navigator: {} };
  vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), ctx);
  const { telemetryBody } = mod.exports;
  assert.deepEqual(JSON.parse(telemetryBody('geo_error', { code: 1 })), { event: 'geo_error', fields: { code: 1 } });
  assert.equal(telemetryBody('nav_tap', { app: 'waze', hidden: false }) !== null, true);
  for (const bad of [{ lat: 1 }, { lng: 1 }, { Latitude: 1 }, { x: { coords: {} } }, { arr: [{ longitude: 2 }] }]) {
    assert.equal(telemetryBody('page_view', bad), null, JSON.stringify(bad));
  }
});

test('a server-held simulated drive never asks the phone and asks without a position', () => {
  const calls = []; let watched = 0;
  const hud = fakeEl(); hud.getAttribute = (k) => (k === 'data-sim' ? '1' : (k === 'data-url' ? '/driver/hud.json' : null));
  const ctx = {
    document: { getElementById: (id) => (id === 'hud' ? hud : fakeEl()), createElement: () => fakeEl(), hidden: false, addEventListener() {} },
    navigator: { onLine: true, geolocation: { watchPosition() { watched++; return 1; } } },
    window: { addEventListener() {}, location: { search: '', pathname: '/driver' } },
    setInterval() { return 1; }, setTimeout() { return 1; },
    fetch(url) { calls.push(url); return new Promise(() => {}); },
    encodeURIComponent, Date, Math, JSON, Promise, console,
  };
  vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), ctx);
  assert.equal(watched, 0, 'navigator.geolocation is not called');
  assert.equal(calls[0], '/driver/hud.json');
});

function loadExports() {
  const mod = { exports: {} };
  const ctx = { module: mod, document: { getElementById: () => null, addEventListener() {} }, window: { addEventListener() {} }, navigator: {} };
  vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), ctx);
  return mod.exports;
}

test('isMoving: speed above 2 m/s, or more than 200 m in the last 120 s, and not otherwise', () => {
  const { isMoving } = loadExports();
  const a = { lat: 44.2, lng: -76.5, t: 0 };
  assert.equal(isMoving(null, { ...a, t: 5, speed: 3 }), true, 'speed alone');
  assert.equal(isMoving(null, { ...a, t: 5, speed: 1.9 }), false, 'slow and no history');
  assert.equal(isMoving(a, { lat: 44.2, lng: -76.5, t: 60000, speed: 0 }), false, 'parked');
  assert.equal(isMoving(a, { lat: 44.2, lng: -76.497, t: 60000, speed: null }), true, 'about 240 m east in a minute');
  assert.equal(isMoving(a, { lat: 44.2, lng: -76.45, t: 130000, speed: null }), false, 'too long ago to count');
  assert.equal(isMoving(a, { lat: 44.2, lng: -76.4995, t: 60000, speed: null }), false, 'under 200 m is drift');
});

test('the nav tap and its outcome are two shapes, and the tap no longer carries hidden', () => {
  const { navTapBody, navResultBody } = loadExports();
  assert.deepEqual(JSON.parse(navTapBody('waze', 'trip')), { event: 'nav_tap', fields: { app: 'waze', mode: 'trip' } });
  assert.deepEqual(JSON.parse(navResultBody('apple', 'destined', true)), { event: 'nav_result', fields: { app: 'apple', mode: 'destined', hidden: true } });
  assert.equal(JSON.parse(navResultBody('google', 'trip', false)).fields.hidden, false);
});

test('the card\'s Ask Chester tap unlocks first, then posts source=drivetracks with the HUD\'s phone fix and starts the album', async () => {
  const order = []; let geoOk = null;
  const ctx = {
    document: { getElementById: () => fakeEl(), createElement: () => fakeEl(), hidden: false, addEventListener() {} },
    navigator: { onLine: true, geolocation: { watchPosition(ok) { geoOk = ok; return 1; } } },
    window: { addEventListener() {}, location: { search: '', pathname: '/driver' } },
    setInterval() { return 1; }, setTimeout() { return 1; },
    fetch(url, opts) {
      if (url === '/chester/ask') {
        order.push('post:' + opts.body);
        const body = '{"t":"music","music":[{"album_id":42,"album":"A","artist":"B"}]}\n{"t":"done"}\n';
        return Promise.resolve({ ok: true, text: async () => body });
      }
      return new Promise(() => {});
    },
    encodeURIComponent, Date, Math, JSON, Promise, URLSearchParams, TextDecoder, console,
  };
  ctx.window.fetch = ctx.fetch; ctx.window.document = ctx.document;
  vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), ctx);
  assert.equal(ctx.window.RTHud.fix(), null, 'no fix, nothing to send');
  assert.equal(ctx.window.RTDriveTracks.body(null), 'source=drivetracks');
  geoOk({ coords: { latitude: 30.2672, longitude: -97.7431, accuracy: 10 } });
  const player = { asking: () => order.push('asking'), say: (m) => order.push('say:' + m), start: (a) => order.push('start:' + a.album_id) };
  // what player.js does inside the tap: body from the HUD's fix, then the one function
  const p = ctx.window.RTDriveTracks({ fetch: ctx.fetch, player, body: ctx.window.RTDriveTracks.body(ctx.window.RTHud.fix()) });
  assert.equal(order[0], 'asking', 'the card is unlocked inside the tap, before any request');
  await p;
  assert.equal(order[1], 'post:source=drivetracks&lat=30.2672&lng=-97.7431');
  assert.equal(order[2], 'start:42');
});

test('the stop row: Navigate follows the chosen maps link, Check in only inside the radius', () => {
  const mod = { exports: {} };
  const ctx = { module: mod, document: { getElementById: () => null, addEventListener() {} }, window: { addEventListener() {} }, navigator: {} };
  vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), ctx);
  const stopRowState = (h) => JSON.parse(JSON.stringify(mod.exports.stopRowState(h)));  // out of the vm realm, so deepEqual compares plain objects
  const base = { drive_started: true, has_next: true, nav_to: 'the map point for Birch Gate', waze: 'https://waze.example/w', nav_href: 'https://maps.example/g' };
  const at = stopRowState({ ...base, check_in_id: 7, check_in_name: 'Birch Gate' });
  assert.deepEqual(at.navigate, { href: 'https://maps.example/g', disabled: false, sub: 'to the map point for Birch Gate' });
  assert.deepEqual(at.checkin, { href: '/driver/post?checkpoint=7', disabled: false, sub: 'at Birch Gate' });
  assert.deepEqual(stopRowState(base).checkin, { href: '', disabled: true, sub: 'Not at a stop' });
  assert.deepEqual(stopRowState({ ...base, nav_none: true }).navigate, { href: '', disabled: true, sub: 'No address on file' });
  assert.deepEqual(stopRowState({ ...base, has_next: false }).navigate, { href: '', disabled: true, sub: 'No stop ahead' });
  assert.equal(stopRowState({ ...base, nav_href: '' }).navigate.href, 'https://waze.example/w');
});
