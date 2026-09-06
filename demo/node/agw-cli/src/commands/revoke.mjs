// agw revoke <prefix>
//
// Revoke a single key by its 8-char prefix.

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { loadState } from '../state.mjs';

export const cmd = {
  name: 'revoke',
  description: 'Revoke a single key by prefix',
  usage: 'agw revoke <prefix>  # e.g. agw revoke agw_dPak',
  flags: [
    { name: 'sign', type: 'bool', desc: 'use per-req EIP-191 sig instead of session' },
    { name: 'name', type: 'string', desc: 'wallet name', default: 'agent' },
  ],
  examples: ['agw revoke agw_dPak'],
};

export async function run({ args, flags, cfg }) {
  const prefix = args[0];
  if (!prefix) throw new Error('usage: agw revoke <prefix>');

  const st = loadState();
  let sessionToken = st?.sessionToken;
  let wallet = null;
  if (!sessionToken || flags.sign) {
    wallet = loadWallet({ name: flags.name });
  }
  const client = new AGWClient({ gateway: st?.gateway });
  const r = await client.revokeKey(prefix, { sessionToken, wallet });

  console.log('revoke');
  if (r.ok) {
    console.log(`  ok=true  prefix=${r.prefix}  hash=${r.hash}`);
    // If we just revoked the active key in state, clear it.
    if (st?.apiKeyPrefix === prefix) {
      st.apiKey = null;
      st.apiKeyPrefix = null;
    }
  } else {
    console.log(`  ok=false  reason=${r.reason}`);
  }
}
