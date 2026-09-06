// agw sessions
//
// List the current user's active sessions.

import { AGWClient } from '../client.mjs';
import { loadState } from '../state.mjs';

export const cmd = {
  name: 'sessions',
  description: 'List active sessions for the current user',
  usage: 'agw sessions',
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  if (!st?.sessionToken) {
    console.log('sessions');
    console.log('  (no active session in state — run `agw login` first)');
    return;
  }
  const client = new AGWClient({ gateway: st.gateway });
  const r = await client.listSessions(st.sessionToken);
  const sessions = r.sessions || r.items || [];

  console.log(`sessions (${r.count ?? sessions.length}):`);
  for (const s of sessions) {
    // The server returns expiresAt/idleExpiresAt as ISO strings
    const exp = s.expiresAt || '(unknown)';
    const idle = s.idleExpiresAt || '(unknown)';
    const here = s.hint && st.sessionToken.startsWith(s.hint) ? ' ← current' : '';
    console.log(`  ${s.hint}  expires=${exp}  idle=${idle}${here}`);
  }
}
