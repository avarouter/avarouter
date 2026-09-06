// 02-login.mjs
//
// Sign in via /v1/auth — exchanges a one-time EIP-191 signature
// for a long-lived session token. Use the token for subsequent
// management calls (no signing required).
//
// Usage:
//   AGW_PRIVATE_KEY=0x... node examples/02-login.mjs [ttlSeconds]

import { AGWClient, loadWallet, describeWallet } from './lib.mjs';

const ttl = parseInt(process.argv[2] || '3600', 10);

const wallet = loadWallet({ name: 'alice' });
const client = new AGWClient();

console.log('login');
console.log('  user:    ', describeWallet(wallet).addressLower);
console.log('  ttl:     ', ttl, 'seconds');

const r = await client.login(wallet, { ttlSeconds: ttl });
console.log('  token:   ', r.sessionToken);
console.log('  expires: ', new Date(r.expiresAt * 1000).toISOString());
console.log('  idleExp: ', new Date(r.idleExpiresAt * 1000).toISOString());
console.log();
console.log('export AGW_SESSION_TOKEN=' + r.sessionToken);
