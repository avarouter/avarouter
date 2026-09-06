// 05-check-balance.mjs
//
// Look up a user's balance. Three modes:
//   (a) own balance via session token
//   (b) own balance via X-Payer (signed)
//   (c) admin: any user via ?user=0x...
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/05-check-balance.mjs
//   AGW_PRIVATE_KEY=0x... node examples/05-check-balance.mjs
//   AGW_SESSION_TOKEN=ags_... node examples/05-check-balance.mjs 0xAlice...

import { AGWClient, loadWallet } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
const pk           = process.env.AGW_PRIVATE_KEY || process.env.AGW_PK;
const user         = process.argv[2];

const client = new AGWClient();
const opts = { user };
if (!user) {
  if (sessionToken) opts.sessionToken = sessionToken;
  else if (pk)      opts.wallet = loadWallet({ name: 'alice' });
  else {
    console.error('need AGW_SESSION_TOKEN or AGW_PRIVATE_KEY, or pass a user arg');
    process.exit(1);
  }
}

const r = await client.balance(opts);
console.log('balance');
console.log('  user:    ', r.from);
console.log('  payTo:   ', r.payTo);
console.log('  balance: ', r.balance.toString(), 'micro-USDC');
console.log('  usdc:    $' + r.balanceUSDC.toFixed(6));
console.log('  registered:', r.registered);
