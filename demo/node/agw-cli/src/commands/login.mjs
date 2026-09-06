// agw login [--ttl N] [--name alice]
//
// Sign once to get a session token. Saves to state.

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { saveState, maskToken } from '../state.mjs';

export const cmd = {
  name: 'login',
  description: 'Sign in via /v1/auth (EIP-191 → session token)',
  usage: 'agw login [--ttl <seconds>] [--name alice]',
  needsWallet: true,
  flags: [
    { name: 'ttl',  type: 'string', desc: 'TTL in seconds (max 24h)', default: '3600' },
    { name: 'name', type: 'string', desc: 'wallet name', default: 'agent' },
  ],
  examples: ['agw login', 'agw login --ttl 7200'],
};

export async function run({ args, flags, cfg }) {
  const ttlSeconds = Number(flags.ttl) || 3600;
  const wallet = loadWallet({ name: flags.name });
  const client = new AGWClient();

  const r = await client.login(wallet, { ttlSeconds });

  saveState({
    gateway:          client.gateway,
    user:             r.user || wallet.address.toLowerCase(),
    sessionToken:     r.sessionToken,
    sessionExpiresAt: r.expiresAt,
  });

  const expIso = new Date(r.expiresAt * 1000).toISOString();
  const idleIso = new Date(r.idleExpiresAt * 1000).toISOString();
  console.log('login');
  console.log(`  user:    ${r.user || wallet.address.toLowerCase()}`);
  console.log(`  ttl:     ${ttlSeconds} seconds`);
  console.log(`  token:   ${maskToken(r.sessionToken)}`);
  console.log(`  expires: ${expIso}`);
  console.log(`  idleExp: ${idleIso}`);
}
