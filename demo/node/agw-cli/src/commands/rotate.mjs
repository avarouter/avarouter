// agw rotate
//
// Revoke all of the user's keys and issue a new one.

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { loadState, saveState } from '../state.mjs';

export const cmd = {
  name: 'rotate',
  description: 'Rotate all of the user\'s API keys',
  usage: 'agw rotate [--sign]',
  flags: [
    { name: 'sign', type: 'bool', desc: 'use per-req EIP-191 sig instead of session' },
    { name: 'name', type: 'string', desc: 'wallet name', default: 'agent' },
  ],
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  let sessionToken = st?.sessionToken;
  let wallet = null;
  if (!sessionToken || flags.sign) {
    wallet = loadWallet({ name: flags.name });
  }
  const client = new AGWClient({ gateway: st?.gateway });
  const r = await client.rotateKeys({ sessionToken, wallet });

  saveState({
    apiKey:          r.key,
    apiKeyPrefix:    r.key.slice(0, 12),
    apiKeyCreatedAt: Math.floor(Date.now() / 1000),
  });

  console.log('rotate');
  console.log(`  new key: ${r.key}`);
  console.log(`  prefix:  ${r.key.slice(0, 12)}`);
  console.log(`  ⚠️  ${r.warning || 'this is the only time the plaintext key will be shown — store it now'}`);
}
