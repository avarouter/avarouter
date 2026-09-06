// Smoke test for the agw CLI.
//
// Assumes a local AGW server is running at $AGW_GATEWAY (default localhost:8080).
// Cleans up state at the start of each run so tests are idempotent.

import { spawn } from 'node:child_process';
import { existsSync, unlinkSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const cli = resolve(here, '..', 'bin', 'agw.mjs');
const stateDir = process.env.AGW_STATE_DIR || resolve(process.env.HOME || '/root', '.agw');
const stateFile = resolve(stateDir, 'state.json');

let pass = 0, fail = 0;

function run(args, opts = {}) {
  return new Promise((resolveP) => {
    const child = spawn('node', [cli, ...args], {
      stdio: ['ignore', 'pipe', 'pipe'],
      ...opts,
    });
    let out = '', err = '';
    child.stdout.on('data', (d) => out += d);
    child.stderr.on('data', (d) => err += d);
    child.on('exit', (code) => resolveP({ code, out, err }));
  });
}

function expect(cond, msg) {
  if (cond) {
    console.log(`  ✓ ${msg}`);
    pass++;
  } else {
    console.log(`  ✗ ${msg}`);
    fail++;
  }
}

async function withCleanState(fn) {
  if (existsSync(stateFile)) unlinkSync(stateFile);
  await fn();
}

async function main() {
  // 1. help
  console.log('— help —');
  const h = await run(['help']);
  expect(h.code === 0, 'agw help exits 0');
  expect(h.out.includes('topup') && h.out.includes('balance'), 'help lists all commands');

  // 2. config
  console.log('— config —');
  const c = await run(['config']);
  expect(c.code === 0, 'agw config exits 0');
  expect(c.out.includes('gateway'), 'config prints gateway');

  // 3. flow (full onboarding)
  await withCleanState(async () => {
    console.log('— flow (full onboarding) —');
    const env = {
      ...process.env,
      AGW_GATEWAY: process.env.AGW_GATEWAY || 'http://127.0.0.1:8080',
      AGW_PRIVATE_KEY: '0x4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6',
    };
    const f = await run(['flow', '1000'], { env });
    expect(f.code === 0, `agw flow exits 0 (got ${f.code}, stderr: ${f.err})`);
    expect(f.out.includes('topup'), 'flow output shows topup step');
    expect(f.out.includes('login'), 'flow output shows login step');
    expect(f.out.includes('mint'), 'flow output shows mint step');
    expect(f.out.includes('chat'), 'flow output shows chat step');
  });

  // 4. whoami (state has user from flow)
  console.log('— whoami —');
  const w = await run(['whoami']);
  expect(w.code === 0, 'agw whoami exits 0');
  expect(w.out.includes('user:'), 'whoami prints user');

  // 5. balance (via session)
  console.log('— balance —');
  const b = await run(['balance']);
  expect(b.code === 0, 'agw balance exits 0');
  expect(b.out.includes('balance:'), 'balance prints balance field');

  // 6. keys
  console.log('— keys —');
  const k = await run(['keys']);
  expect(k.code === 0, 'agw keys exits 0');
  expect(k.out.includes('active'), 'keys prints active status');

  // 7. sessions
  console.log('— sessions —');
  const s = await run(['sessions']);
  expect(s.code === 0, 'agw sessions exits 0');

  // 8. users
  console.log('— users —');
  const u = await run(['users']);
  expect(u.code === 0, 'agw users exits 0');
  expect(u.out.includes('users ('), 'users prints count');

  // 9. chat
  console.log('— chat (uses saved key) —');
  const c2 = await run(['chat', 'mock-model', 'hi']);
  expect(c2.code === 0, 'agw chat exits 0');
  expect(c2.out.includes('status: 200'), 'chat returns 200');

  // 10. state
  console.log('— state —');
  const st = await run(['state']);
  expect(st.code === 0, 'agw state exits 0');
  expect(st.out.includes('path:'), 'state prints path');

  // 11. unknown command
  console.log('— error handling —');
  const bad = await run(['nonexistent']);
  expect(bad.code !== 0, 'unknown command exits non-zero');

  console.log();
  console.log(`  ${pass} passed, ${fail} failed`);
  if (fail > 0) process.exit(1);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
