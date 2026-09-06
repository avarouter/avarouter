// AGW x402 multi-tenant demo (Node.js + ethers v6) — v5 with sessions.
//
// Demonstrates:
//   - 3 independent users (Alice, Bob, Carol) with per-user payTo
//   - /v1/auth: sign once, get a session token
//   - Management calls (POST /v1/keys, DELETE /v1/keys/{p},
//     POST /v1/keys/rotate) use the session token
//   - Per-user / per-key stats
//   - Cross-user isolation
//   - /v1/auth/logout invalidates the session
//   - Topup still requires per-request EIP-191 signature (the
//     session token doesn't authorise value-bearing operations)

import { ethers } from 'ethers';
import { signedHeaders } from './sign.mjs';

const GATEWAY = process.env.AGW_GATEWAY || 'http://127.0.0.1:8080';
const usdcAddress = '0x5425890298aed601595a70AB815c96711a31Bc65';
const chainId = 43113;

const log = (...a) => console.log(...a);
const ok  = (msg) => log(`  \x1b[32m✓\x1b[0m ${msg}`);
const ko  = (msg) => log(`  \x1b[31m✗\x1b[0m ${msg}`);
const col = (s) => `\x1b[36m${s}\x1b[0m`;
const step = (n, name) => log('\n' + col(`=== Step ${n}: ${name} ===`));
const dim = (s) => `\x1b[2m${s}\x1b[0m`;

const bearer = (key) => ({ Authorization: `Bearer ${key}` });
const session = (tok) => ({ 'X-AGW-Session': tok });

function makeUser(name, envKey) {
  let wallet;
  if (process.env[envKey]) {
    wallet = new ethers.Wallet(process.env[envKey]);
  } else {
    const seed = ethers.id('agw-demo-' + name);
    wallet = new ethers.Wallet(seed);
  }
  const payTo = new ethers.Wallet(ethers.id('agw-payTo-' + name)).address;
  return {
    name,
    from:    wallet.address.toLowerCase(),
    payTo:   payTo.toLowerCase(),
    wallet,
  };
}

const USERS = {
  alice: makeUser('alice', 'ALICE_PK'),
  bob:   makeUser('bob',   'BOB_PK'),
  carol: makeUser('carol', 'CAROL_PK'),
};

// ---------------- HTTP ----------------

async function httpReq(method, path, body, hdrs = {}) {
  const r = await fetch(`${GATEWAY}${path}`, {
    method,
    headers: { 'Content-Type': 'application/json', ...hdrs },
    body: body !== undefined && body !== null ? (typeof body === 'string' ? body : JSON.stringify(body)) : undefined,
  });
  const text = await r.text();
  let json = null;
  try { json = JSON.parse(text); } catch {}
  return { status: r.status, json, text };
}

async function httpSigned(user, method, path, body) {
  const bodyStr = body !== undefined && body !== null
    ? (typeof body === 'string' ? body : JSON.stringify(body))
    : '';
  const hdr = await signedHeaders(user.wallet, method, path, bodyStr);
  return httpReq(method, path, body, hdr);
}

const balance = async (user) => {
  const r = await httpReq('GET', `/payments/balance?user=${user.from}`, null, {});
  if (r.status !== 200) return null;
  return BigInt(r.json.balance);
};

// ---------------- x402 topup ----------------

async function signX402Transfer({ from, to, value, validAfter, validBefore, nonce }, wallet) {
  const domain = {
    name: 'USD Coin', version: '2', chainId, verifyingContract: usdcAddress,
  };
  const types = {
    TransferWithAuthorization: [
      { name: 'from',        type: 'address' },
      { name: 'to',          type: 'address' },
      { name: 'value',       type: 'uint256' },
      { name: 'validAfter',  type: 'uint256' },
      { name: 'validBefore', type: 'uint256' },
      { name: 'nonce',       type: 'bytes32' },
    ],
  };
  return wallet.signTypedData(domain, types, {
    from, to, value, validAfter, validBefore, nonce,
  });
}

async function topup(user, amountMicro) {
  // 1. signed challenge request
  const r1 = await httpSigned(user, 'POST', '/v1/topup',
    { amount: String(amountMicro), payTo: user.payTo });
  if (r1.status !== 402) {
    ko(`${user.name}: expected 402, got ${r1.status} ${r1.text}`);
    return null;
  }
  const accepts = r1.json?.accepts?.[0];
  if (!accepts) { ko(`${user.name}: no accepts in 402`); return null; }
  const { payTo, maxAmountRequired, extra = {} } = accepts;
  const amount = maxAmountRequired;
  const va = BigInt(extra.validAfter  ?? 0);
  const vb = BigInt(extra.validBefore ?? 0);
  const nonce = extra.nonce
    ?? '0x' + [...crypto.getRandomValues(new Uint8Array(32))]
        .map((b) => b.toString(16).padStart(2, '0')).join('');

  // 2. sign + signed retry
  const sig = await signX402Transfer({
    from: user.from, to: payTo,
    value: amount, validAfter: va, validBefore: vb, nonce,
  }, user.wallet);
  const parsed = ethers.Signature.from(sig);
  const r2 = await httpSigned(user, 'POST', '/v1/topup', {
    amount,
    payTo,
    payment: {
      from:        user.from,
      to:          payTo,
      value:       amount,
      validAfter:  String(va),
      validBefore: String(vb),
      nonce,
      v:           parsed.v,
      r:           parsed.r,
      s:           parsed.s,
    },
  });
  if (r2.status !== 200) {
    ko(`${user.name}: topup failed: ${r2.status} ${r2.text}`);
    return null;
  }
  return BigInt(r2.json.balance);
}

// ---------------- auth ----------------

// Sign an AGW-AUTH-v1 challenge and exchange it for a session token.
async function login(user, ttlSeconds = 3600) {
  const expiresAt = Math.floor(Date.now() / 1000) + ttlSeconds;
  const body = { from: user.from, expiresAt };
  // The signed message is AGW-AUTH-v1 ... (built server-side from
  // body). On the client we replicate the same buildAuthMessage.
  const ts = Math.floor(Date.now() / 1000);
  const { buildAuthMessage } = await import('./sign.mjs');
  const msg = buildAuthMessage(user.from, expiresAt, ts, user.from);
  const sig = await user.wallet.signMessage(msg);
  const hdr = {
    'X-Payer':         user.from,
    'X-AGW-Timestamp': String(ts),
    'X-AGW-Signature': sig,
  };
  const r = await httpReq('POST', '/v1/auth', body, hdr);
  if (r.status !== 200) { ko(`${user.name}: login failed: ${r.status} ${r.text}`); return null; }
  return r.json.sessionToken;
}

async function logout(token) {
  return httpReq('POST', '/v1/auth/logout', null, session(token));
}

// Management call using the session token (no signature required).
const auth = (user) => async (method, path, body) => {
  if (!user.token) throw new Error(`${user.name} not logged in`);
  return httpReq(method, path, body, session(user.token));
};

// ---------------- hot path (bearer) ----------------

async function chatCall(key) {
  return httpReq('POST', '/v1/chat/completions',
    { model: 'mock-model', messages: [{ role: 'user', content: 'hi' }] },
    bearer(key));
}

// =====================================================================
//  Main flow
// =====================================================================
log(col('Multi-tenant AGW demo (sessions) — Alice, Bob, Carol'));
log('');
for (const [name, u] of Object.entries(USERS)) {
  log(`  ${name.padEnd(6)} from=${dim(u.from)}  payTo=${dim(u.payTo)}`);
}

step(1, 'Server starts in multi-tenant mode (0 users)');
const users = await httpReq('GET', '/payments/users', null, {});
if (users.json.count !== 0) { ko(`expected 0 users, got ${users.json.count}`); process.exit(1); }
ok('clean state');

step(2, 'Each user topups (per-request EIP-191) — topup NEVER uses sessions');
const topupAmounts = { alice: 1000, bob: 800, carol: 500 };
for (const [name, u] of Object.entries(USERS)) {
  const bal = await topup(u, topupAmounts[name]);
  if (bal == null) process.exit(1);
  ok(`${name} topup → balance ${bal}`);
}

step(3, 'Each user logs in ONCE via /v1/auth — gets a session token');
const sessions = {};
for (const [name, u] of Object.entries(USERS)) {
  const tok = await login(u);
  if (!tok) process.exit(1);
  sessions[name] = tok;
  u.token = tok; // attach to user for auth() helper
  ok(`${name} session: ${tok.slice(0,12)}… (expires in 1h)`);
}

step(4, 'Management calls now use the session token — NO signing required');
for (const [name, u] of Object.entries(USERS)) {
  // generate 2 keys per user, using session auth
  for (let i = 0; i < 2; i++) {
    const a = auth(u);
    const r = await a('POST', '/v1/keys', {});
    if (r.status !== 200) { ko(`${name} key ${i}: ${r.status} ${r.text}`); process.exit(1); }
    if (!u.keys) u.keys = [];
    u.keys.push(r.json.key);
  }
  ok(`${name} minted 2 keys via session: ${u.keys[0].slice(0,8)}, ${u.keys[1].slice(0,8)}`);
}

step(5, 'Each user spends 2 calls with their first key — independent drains');
for (const [name, u] of Object.entries(USERS)) {
  for (let i = 0; i < 2; i++) {
    const r = await chatCall(u.keys[0]);
    if (r.status !== 200) { ko(`${name} call ${i}: ${r.status} ${r.text}`); process.exit(1); }
  }
  const bal = await balance(u);
  log(`  ${name}: 2 calls done, balance now ${bal}`);
}
ok('all three users charged independently');

step(6, 'AUTH: invalid session token is rejected');
const fakeHdr = { 'X-AGW-Session': 'ags_totallynotreal' };
const fake = await httpReq('POST', '/v1/keys', {}, fakeHdr);
if (fake.status !== 401) { ko(`fake token should be 401, got ${fake.status}`); process.exit(1); }
ok(`invalid session token correctly rejected`);

step(7, 'AUTH: cross-user session — Bob\'s token used with X-Payer=alice');
const crossHdr = { ...session(sessions.bob), 'X-Payer': USERS.alice.from };
const cross = await httpReq('POST', '/v1/keys', {}, crossHdr);
if (cross.status !== 401) { ko(`cross-user session should be 401, got ${cross.status}`); process.exit(1); }
ok(`cross-user (bob's session, X-Payer=alice) correctly rejected`);

step(8, 'AUTH: per-request signature still works (backward-compat path)');
// Alice signs a request directly (no session) — should still work.
const sigHdr = await signedHeaders(USERS.alice.wallet, 'GET', '/v1/keys', null);
const sig = await httpReq('GET', '/v1/keys', null, sigHdr);
if (sig.status !== 200) { ko(`per-request sig should still work: ${sig.status}`); process.exit(1); }
ok('per-request EIP-191 signature still works alongside sessions');

step(9, 'Cross-user isolation: Bob (session) cannot revoke Alice\'s key');
const aliceKey1Prefix = USERS.alice.keys[0].slice(0, 8);
const bobRevoke = await auth(USERS.bob)('DELETE', `/v1/keys/${aliceKey1Prefix}`, null);
if (bobRevoke.status !== 404) { ko(`Bob should get 404, got ${bobRevoke.status}`); process.exit(1); }
ok(`Bob (session) cannot revoke Alice's key — 404`);

step(10, 'Granular revoke: Alice (session) revokes one of her own keys');
const aliceKey2Prefix = USERS.alice.keys[1].slice(0, 8);
const aliceRevoke = await auth(USERS.alice)('DELETE', `/v1/keys/${aliceKey2Prefix}`, null);
if (aliceRevoke.status !== 200) { ko(`alice revoke: ${aliceRevoke.status}`); process.exit(1); }
ok(`Alice revoked her own key (no signature needed, just session)`);

step(11, 'List active sessions per user (Alice)');
const aliceSessions = await auth(USERS.alice)('GET', '/v1/auth/sessions', null);
if (aliceSessions.status !== 200) { ko(`list sessions: ${aliceSessions.status}`); process.exit(1); }
log(`  alice has ${aliceSessions.json.count} active session(s)`);
for (const s of aliceSessions.json.sessions) {
  log(`    ${s.hint}  exp=${s.expiresAt}`);
}
ok('session list works');

step(12, 'Drain Bob, logout Bob — then his token is dead');
const bobKey = USERS.bob.keys[0];
for (let i = 0; i < 6; i++) {
  const r = await chatCall(bobKey);
  if (r.status !== 200) { ko(`bob drain ${i}: ${r.status}`); process.exit(1); }
}
const bobDrain = await chatCall(bobKey);
if (bobDrain.status !== 402) { ko(`bob drain should 402, got ${bobDrain.status}`); process.exit(1); }
ok('bob drained → 402');
const bobLogout = await logout(sessions.bob);
if (bobLogout.status !== 200) { ko(`logout: ${bobLogout.status}`); process.exit(1); }
ok(`bob logged out: ${bobLogout.json.revoked}`);
// Bob's old token should now be rejected.
const deadSession = await auth(USERS.bob)('POST', '/v1/keys', {});
if (deadSession.status !== 401) { ko(`revoked session should 401, got ${deadSession.status}`); process.exit(1); }
ok('revoked session correctly rejected');

step(13, 'Final per-user + per-key rollup');
const final = await httpReq('GET', '/payments/users', null, {});
log(`  ${final.json.count} users total`);
for (const row of final.json.items) {
  log(`    ${row.from.slice(0,10)}…  bal=${row.balance}  reqs=${row.requestCount}  $${row.totalCostUSDC.toFixed(4)}`);
}
const allKeys = await httpReq('GET', '/payments/keys', null, {});
log(`  ${allKeys.json.count} keys total`);

log('\n=== Demo complete ===');
