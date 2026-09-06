// 08-logout.mjs
//
// Invalidate a session token. Idempotent: logging out a dead
// session returns ok=true.
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/08-logout.mjs

import { AGWClient } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
if (!sessionToken) {
  console.error('need AGW_SESSION_TOKEN');
  process.exit(1);
}

const client = new AGWClient();
const r = await client.logout(sessionToken);
console.log('logout');
console.log('  status:', r.status);
console.log('  body:  ', JSON.stringify(r.json, null, 2));
