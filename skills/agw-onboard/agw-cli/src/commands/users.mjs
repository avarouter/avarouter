// agw users [--window 1h|24h|7d|30d|all]
//
// List all registered users with balances + per-user request aggregates.

import { AGWClient } from '../client.mjs';
import { loadState } from '../state.mjs';

export const cmd = {
  name: 'users',
  description: 'List all registered users',
  usage: 'agw users [--window 1h|24h|7d|30d|all]',
  flags: [
    { name: 'window', type: 'string', desc: 'aggregation window', default: 'all' },
  ],
  examples: ['agw users', 'agw users --window 24h'],
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  const client = new AGWClient({ gateway: st?.gateway });
  const r = await client.listUsers({ window: flags.window });
  const items = r.items || [];

  console.log(`users (${r.count ?? items.length} total, window=${r.window || 'all'}):`);
  for (const u of items) {
    const bal = u.balance ?? 0;
    const reqs = u.reqs ?? 0;
    const err = u.err ?? 0;
    const last = u.last ? new Date(u.last).toISOString() : 'never';
    const short = (u.from || '').slice(0, 10);
    const payTo = (u.payTo || '').slice(0, 10);
    console.log(`  ${short}…  payTo=${payTo}…  bal=${bal}  reqs=${reqs}  err=${err}  $${u.costUSDC ?? 0}  last=${last}`);
  }
}
