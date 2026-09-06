// agw logout [--all]
//
// Invalidate the current session (or all sessions if --all).

import { AGWClient } from '../client.mjs';
import { loadState, clearState } from '../state.mjs';

export const cmd = {
  name: 'logout',
  description: 'Revoke the current (or all) session tokens',
  usage: 'agw logout',
  flags: [],
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  if (!st?.sessionToken) {
    console.log('logout');
    console.log('  (no active session in state)');
    return;
  }
  const client = new AGWClient({ gateway: st.gateway });
  const r = await client.logout(st.sessionToken);
  // clear state (preserve user identity for next login)
  if (r.status === 200) {
    clearState();
    console.log('logout');
    console.log(`  ok=true  revoked=${st.sessionToken.slice(0, 12)}…`);
  } else {
    console.log('logout');
    console.log(`  status=${r.status}  text=${r.text}`);
  }
}
