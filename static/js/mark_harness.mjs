// Reads {kind, code, saved} on stdin and prints the status line the player shows
// after a Favourite or Mark, from the page's own markStatus (static/js/player.js).
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const ctx = { module: { exports: {} }, document: { getElementById: () => null }, encodeURIComponent, Math, JSON, Object };
vm.runInNewContext(readFileSync(new URL('./player.js', import.meta.url), 'utf8'), ctx);
const i = JSON.parse(readFileSync(0, 'utf8'));
const reply = i.saved === undefined ? undefined : { saved: i.saved };
process.stdout.write(JSON.stringify({ status: ctx.module.exports.markStatus(i.kind, i.code, reply) }));
