// agw keys [--user 0x...] [--name alice]
//
// List API keys. Default: own keys (via session or sig).
// With --user 0x...: admin query.

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { loadState } from '../state.mjs';

export const cmd = {
  name: 'keys',
  description: 'List API keys (own or any user)',
  usage: 'agw keys [--user 0x...]',
  flags: [
    { name: 'user', type: 'string', desc: 'admin: list another user\'s keys' },
    { name: 'name', type: 'string', desc: 'wallet name', default: 'agent' },
  ],
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  const client = new AGWClient({ gateway: st?.gateway });
  const opts = {};
  if (flags.user) {
    opts.user = flags.user;
  } else if (st?.sessionToken) {
    opts.sessionToken = st.sessionToken;
  } else {
    const wallet = loadWallet({ name: flags.name });
    opts.wallet = wallet;
  }
  const r = await client.listKeys(opts);

  const items = r.items || [];
  console.log(`keys (${items.length}):`);
  for (const k of items) {
    const status = k.active === true ? 'active' : (k.active === false ? 'revoked' : 'unknown');
    const req = k.req ?? 0;
    const err = k.err ?? 0;
    const last = k.last ? new Date(k.last).toISOString() : 'never';
    const issued = k.issuedAt ? new Date(k.issuedAt).toISOString() : '';
    console.log(`  ${k.prefix}  ${status}  reqs=${req}  err=${err}  issued=${issued}`);
  }
}
