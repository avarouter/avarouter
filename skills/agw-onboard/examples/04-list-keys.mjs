// 04-list-keys.mjs
//
// List all of the user's API keys (active + revoked), with
// per-key request counts and cost aggregates.
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/04-list-keys.mjs
//   # admin: see any user's keys
//   AGW_SESSION_TOKEN=ags_... node examples/04-list-keys.mjs 0xAlice...

import { AGWClient } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
const user         = process.argv[2];

if (!sessionToken) {
  console.error('need AGW_SESSION_TOKEN');
  process.exit(1);
}

const client = new AGWClient();
const r = await client.listKeys({ sessionToken, user });

console.log('list-keys', user ? `(user=${user})` : '(self)');
console.log(`  ${r.count} keys:`);
for (const k of r.items) {
  const status = k.active ? '✓ active ' : '✗ revoked';
  console.log(
    `    ${k.prefix}  ${status}  reqs=${k.requestCount}  in=${k.totalTokensIn}  out=${k.totalTokensOut}  $${k.totalCostUSDC.toFixed(6)}`
  );
}
