// 09-list-users.mjs
//
// List all registered users with their balances and per-user
// request/cost aggregates. Open endpoint — no auth required.
//
// Usage:
//   node examples/09-list-users.mjs
//   node examples/09-list-users.mjs 24h         # time window

import { AGWClient } from './lib.mjs';

const window = process.argv[2]; // optional: 1h|24h|7d|30d|all
const client = new AGWClient();
const r = await client.listUsers({ window });

console.log('list-users', window ? `(window=${window})` : '(all time)');
console.log(`  ${r.count} users:`);
for (const u of r.items) {
  console.log(
    `    ${u.from.slice(0, 10)}…  payTo=${u.payTo.slice(0, 10)}…  ` +
    `bal=${u.balance}  reqs=${u.requestCount}  err=${u.errorCount}  ` +
    `$${u.totalCostUSDC.toFixed(4)}  last=${u.lastUsed || 'never'}`
  );
}
