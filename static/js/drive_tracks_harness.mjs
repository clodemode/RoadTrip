// Runs hud.js's driveTracks() against one answer from the real /chester/ask
// route, with a stand-in for the card player, and prints what the card was told.
// Read by drive_tracks_steps_test.go (and handy by hand):
//   echo '{"status":200,"body":"{\"t\":\"done\"}\n"}' | node static/js/drive_tracks_harness.mjs
// Input: {status, body, chunked, throws}. chunked splits the body in the middle
// of lines, as a phone's network does. Output: {asked, said, started, posted}.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const input = JSON.parse(readFileSync(0, 'utf8'));
const mod = { exports: {} };
vm.runInNewContext(readFileSync(new URL('./hud.js', import.meta.url), 'utf8'), {
  module: mod, document: { getElementById: () => null, addEventListener() {} }, window: { addEventListener() {} }, navigator: {},
  TextDecoder, JSON, Promise,
});
const out = { asked: [], said: [], started: [], posted: [] };
const player = {
  asking: (m) => out.asked.push(m),
  say: (m) => out.said.push(m),
  start: (a) => out.started.push(a),
};
function response() {
  const ok = input.status >= 200 && input.status < 300;
  const resp = { ok, json: async () => JSON.parse(input.body), text: async () => input.body };
  if (input.chunked) {
    const bytes = new TextEncoder().encode(input.body);
    let i = 0;
    resp.body = { getReader: () => ({ read: async () => (i >= bytes.length ? { done: true } : { done: false, value: bytes.slice(i, (i += 7)) }) }) };
  }
  return resp;
}
const { driveTracks } = mod.exports;
await driveTracks({
  fetch: async (url, opts) => {
    out.posted.push({ url, method: opts.method, body: opts.body });
    if (input.throws) throw new Error('offline');
    return response();
  },
  player,
  body: input.posted || 'source=drivetracks',
});
console.log(JSON.stringify(out));
