// agw mint
//
// Mint a new API key. Uses session token from state, or signs per-req
// if --sign flag is given (still requires wallet).

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { loadState, saveState, maskToken } from '../state.mjs';

export const cmd = {
  name: 'mint',
  description: 'Mint a new API key (saved to state)',
  usage: 'agw mint [--sign]',
  flags: [
    { name: 'sign', type: 'bool', desc: 'use per-req EIP-191 sig instead of session token' },
    { name: 'name', type: 'string', desc: 'wallet name', default: 'agent' },
  ],
  examples: ['agw mint', 'agw mint --sign'],
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  let sessionToken = st?.sessionToken;
  let wallet = null;
  if (!sessionToken || flags.sign) {
    wallet = loadWallet({ name: flags.name });
  }
  const client = new AGWClient({ gateway: st?.gateway });

  const r = await client.mintKey({ sessionToken, wallet });

  saveState({
    apiKey:          r.key,
    apiKeyPrefix:    r.key.slice(0, 12),
    apiKeyCreatedAt: Math.floor(Date.now() / 1000),
  });

  console.log('mint');
  console.log(`  key:     ${r.key}`);
  console.log(`  prefix:  ${r.key.slice(0, 12)}`);
  console.log(`  ⚠️  ${r.warning || 'this is the only time the plaintext key will be shown — store it now'}`);
}
