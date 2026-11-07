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
