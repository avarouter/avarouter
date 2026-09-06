// 10-list-sessions.mjs
//
// List the user's active sessions (so they can see where they're
// signed in, and revoke the ones they don't recognize).
//
// Usage:
//   AGW_SESSION_TOKEN=ags_... node examples/10-list-sessions.mjs

import { AGWClient } from './lib.mjs';

const sessionToken = process.env.AGW_SESSION_TOKEN;
if (!sessionToken) {
  console.error('need AGW_SESSION_TOKEN');
  process.exit(1);
}

const client = new AGWClient();
const r = await client.listSessions(sessionToken);

console.log('list-sessions');
console.log(`  user: ${r.user}`);
console.log(`  ${r.count} active session(s):`);
for (const s of r.sessions) {
  console.log(`    ${s.hint}  exp=${s.expiresAt}  lastSeen=${s.lastSeen}`);
}
