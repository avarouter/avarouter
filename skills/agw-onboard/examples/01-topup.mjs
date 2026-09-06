// 01-topup.mjs
//
// Single-call example: topup the user's balance. On first call,
// the user is auto-registered (payTo defaults to the user's
// wallet address).
//
// Usage:
//   AGW_PRIVATE_KEY=0x... node examples/01-topup.mjs [amountMicro]
//   AGW_PRIVATE_KEY=0x... AGW_GATEWAY=https://avarouter.up.railway.app \
//     node examples/01-topup.mjs 1000
//   # or with a .env file (see 99-full-flow.mjs for format)

import { AGWClient, loadWallet, describeWallet } from './lib.mjs';

const amount = BigInt(process.argv[2] || '1000');

const wallet = loadWallet({ name: 'alice' });
const client = new AGWClient();

const cfg = client.describe();
console.log('topup');
console.log('  gateway: ', cfg.gateway);
console.log('  user:    ', describeWallet(wallet).addressLower);
console.log('  amount:  ', amount.toString(), 'micro-USDC');

const r = await client.topup({ wallet, amount: amount.toString() });
console.log('  balance: ', r.balance.toString());
console.log('  payTo:   ', r.payTo);
console.log('  note:    ', r.note || '(on-chain settlement)');
