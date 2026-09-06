// 06-rotate-key.mjs
//
// Revoke ALL of the user's active keys and issue a fresh one.
// Use this when a key is leaked.
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/06-rotate-key.mjs
//   # or
//   AGW_PRIVATE_KEY=0x... node examples/06-rotate-key.mjs

import { AGWClient, loadWallet } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
const pk           = process.env.AGW_PRIVATE_KEY || process.env.AGW_PK;

if (!sessionToken && !pk) {
  console.error('need AGW_SESSION_TOKEN or AGW_PRIVATE_KEY');
  process.exit(1);
}

const client = new AGWClient();
const wallet = pk ? loadWallet({ name: 'alice' }) : null;

console.log('rotate-key');
const r = await client.rotateKeys({ sessionToken, wallet });
console.log('  new key: ', r.key);
console.log('  prefix:  ', r.meta.prefix);
console.log('  owner:   ', r.meta.owner);
console.log();
console.log('  ⚠️  ' + r.warning);
console.log();
console.log('export AGW_API_KEY=' + r.key);
