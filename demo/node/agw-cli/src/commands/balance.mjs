// agw balance [--user 0x...] [--name alice]
//
// Look up a balance:
//   - with --user 0x...         admin lookup (no auth)
//   - with state.sessionToken   own balance via session
//   - with --name <wallet>      own balance via X-Payer sig
//   - default: use whatever is in state

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { loadState } from '../state.mjs';

export const cmd = {
  name: 'balance',
  description: 'Look up a balance (3 modes: admin, session, sig)',
  usage: 'agw balance [--user 0x...] [--name alice]',
  flags: [
    { name: 'user', type: 'string', desc: 'admin: query another user\'s balance' },
    { name: 'name', type: 'string', desc: 'wallet name (X-Payer mode)', default: 'agent' },
  ],
  examples: [
    'agw balance',
    'agw balance --user 0xAlice...',
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
  } else if (st?.user) {
    // Use the user's address from state to query via ?user=
    opts.user = st.user;
  } else {
    // Fall back to per-req sig
    const wallet = loadWallet({ name: flags.name });
    opts.wallet = wallet;
  }
  const r = await client.balance(opts);

  console.log('balance');
  if (r.from) {
    console.log(`  user:       ${r.from}`);
    console.log(`  payTo:      ${r.payTo}`);
    console.log(`  balance:    ${r.balance.toString()} micro-USDC`);
    console.log(`  usdc:       $${r.balanceUSDC}`);
    console.log(`  registered: ${r.registered}`);
  } else {
    // Aggregate view (no auth, no X-Payer, no session)
    console.log(`  (aggregate — no user context)`);
    console.log(`  total:      ${r.balance.toString()} micro-USDC across ${r.userCount ?? '?'} users`);
    console.log(`  usdc:       $${r.balanceUSDC}`);
    console.log(`  network:    ${r.network || '(unknown)'}`);
  }
}
