// 03-mint-key.mjs
//
// Mint a new API key. Requires either a session token
// (X-AGW-Session) or a per-request signature (wallet).
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/03-mint-key.mjs
//   # or
//   AGW_PRIVATE_KEY=0x... node examples/03-mint-key.mjs

import { AGWClient, loadWallet } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
const pk           = process.env.AGW_PRIVATE_KEY || process.env.AGW_PK;

if (!sessionToken && !pk) {
  console.error('need AGW_SESSION_TOKEN or AGW_PRIVATE_KEY');
  process.exit(1);
}

const client = new AGWClient();
const wallet = pk ? loadWallet({ name: 'alice' }) : null;

console.log('mint-key');
const r = await client.mintKey(sessionToken, wallet);
console.log('  key:     ', r.key);
console.log('  prefix:  ', r.meta.prefix);
console.log('  owner:   ', r.meta.owner);
console.log('  issued:  ', r.meta.issuedAt);
console.log();
console.log('  ⚠️  ' + r.warning);
console.log();
console.log('export AGW_API_KEY=' + r.key);
