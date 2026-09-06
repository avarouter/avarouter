// agw flow <amount>
//
// One-shot onboarding: topup → login → mint → chat → (optional logout).
// Saves all artifacts to state so the user can keep going with `agw chat` etc.

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { saveState, maskToken } from '../state.mjs';

export const cmd = {
  name: 'flow',
  description: 'End-to-end: topup → login → mint → chat',
  usage: 'agw flow <amount> [--name alice] [--logout]',
  needsWallet: true,
  args: [
    { name: 'amount', required: true, type: 'string', desc: 'amount in micro-USDC' },
  ],
  flags: [
    { name: 'name',   type: 'string', desc: 'wallet name', default: 'agent' },
    { name: 'logout', type: 'bool',   desc: 'logout after the demo chat' },
  ],
  examples: [
    'agw flow 1000',
    'agw flow 500 --name alice --logout',
  ],
};

export async function run({ args, flags, cfg }) {
  const amount = args[0];
  if (!amount) throw new Error('usage: agw flow <amount>');

  const wallet = loadWallet({ name: flags.name });
  const client = new AGWClient();
  const addr = wallet.address.toLowerCase();
  console.log('=== full flow ===');
  console.log(`  gateway: ${client.gateway}`);
  console.log(`  user:    ${addr}`);
  console.log();

  // 1. topup
  process.stdout.write('[1/5] topup … ');
  const t = await client.topup({ wallet, amount });
  console.log(`ok (balance=${t.balance})`);
  saveState({ gateway: client.gateway, user: t.user, payTo: t.payTo, lastBalance: t.balance.toString() });

  // 2. login
  process.stdout.write('[2/5] login … ');
  const l = await client.login(wallet);
  console.log(`ok (token=${maskToken(l.sessionToken)})`);
  saveState({ sessionToken: l.sessionToken, sessionExpiresAt: l.expiresAt });

  // 3. mint
  process.stdout.write('[3/5] mint-key … ');
  const k = await client.mintKey({ sessionToken: l.sessionToken });
  console.log(`ok (key=${maskToken(k.key, 12)})`);
  saveState({ apiKey: k.key, apiKeyPrefix: k.key.slice(0, 12), apiKeyCreatedAt: Math.floor(Date.now() / 1000) });

  // 4. chat
  process.stdout.write('[4/5] chat … ');
  const c = await client.chat(k.key, {
    model: 'mock-model',
    messages: [{ role: 'user', content: 'hello' }],
  });
  if (c.status !== 200) {
    console.log(`failed (${c.status})`);
    process.exit(1);
  }
  const reply = c.body?.choices?.[0]?.message?.content || JSON.stringify(c.body);
  console.log(`ok (${c.status} — "${reply}")`);

  // 5. logout
  if (flags.logout) {
    process.stdout.write('[5/5] logout … ');
    const lo = await client.logout(l.sessionToken);
    console.log(lo.status === 200 ? 'ok' : `status=${lo.status}`);
  } else {
    console.log('[5/5] logout (skipped — pass --logout to revoke)');
  }

  console.log();
  console.log('=== done ===');
  console.log(`  User:    ${t.user}`);
  console.log(`  API key: ${k.key}`);
  console.log(`  PayTo:   ${t.payTo}`);
  console.log();
  console.log('  Next: `agw chat "hi"`, `agw balance`, `agw users` …');
}
