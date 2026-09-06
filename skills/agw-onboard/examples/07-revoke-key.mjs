// 07-revoke-key.mjs
//
// Revoke a single key by its 8-char prefix. Only the key's owner
// (signed X-Payer or session-bound user) can revoke.
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/07-revoke-key.mjs agw_dPak
//   # or
//   AGW_PRIVATE_KEY=0x... node examples/07-revoke-key.mjs agw_dPak

import { AGWClient, loadWallet } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
const pk           = process.env.AGW_PRIVATE_KEY || process.env.AGW_PK;
const prefix       = process.argv[2];

if (!prefix) {
  console.error('usage: node 07-revoke-key.mjs <prefix>  (e.g. agw_dPak)');
  process.exit(1);
}
if (!sessionToken && !pk) {
  console.error('need AGW_SESSION_TOKEN or AGW_PRIVATE_KEY');
  process.exit(1);
}

const client = new AGWClient();
const wallet = pk ? loadWallet({ name: 'alice' }) : null;

console.log('revoke-key');
const r = await client.revokeKey(prefix, { sessionToken, wallet });
if (!r.ok) {
  console.log('  ✗ not found (or not your key)');
  process.exit(1);
}
console.log('  ✓ revoked', r.prefix, '→', r.hash.slice(0, 16) + '…');
