// agw topup <amount> [--pay-to 0x...]
//
// Topup the user's balance via x402 EIP-3009. Auto-registers on first call.
// Wallet is required (signs twice: EIP-3009 + EIP-191).

import { loadWallet } from '../wallet.mjs';
import { AGWClient } from '../client.mjs';
import { saveState } from '../state.mjs';

export const cmd = {
  name: 'topup',
  description: 'Top up the user balance (EIP-3009 x402)',
  usage: 'agw topup <amount> [--pay-to 0x...] [--name alice]',
  needsWallet: true,
  args: [
    { name: 'amount', required: true, type: 'string', desc: 'amount in micro-USDC' },
  ],
  flags: [
    { name: 'pay-to', type: 'string', desc: 'deposit address (defaults to wallet address)' },
    { name: 'name',    type: 'string', desc: 'wallet name (for <NAME>_PK lookup)', default: 'agent' },
  ],
  examples: [
    'agw topup 1000',
    'agw topup 5000 --pay-to 0x1234...',
  ],
};

export async function run({ args, flags, cfg }) {
  const amount = args[0];
  if (!amount) throw new Error('usage: agw topup <amount>');

  const wallet = loadWallet({ name: flags.name });
  const client = new AGWClient();

  const opts = { wallet, amount };
  if (flags['pay-to']) opts.payTo = flags['pay-to'];

  const r = await client.topup(opts);

  // Save to state so subsequent commands know who we are.
  saveState({
    gateway: client.gateway,
    user:    r.user,
    payTo:   r.payTo,
    lastBalance: r.balance.toString(),
  });

  console.log('topup');
  console.log(`  gateway: ${client.gateway}`);
  console.log(`  user:    ${r.user}`);
  console.log(`  amount:  ${amount} micro-USDC`);
  console.log(`  balance: ${r.balance.toString()}`);
  console.log(`  payTo:   ${r.payTo}`);
  console.log(`  note:    ${r.note || '(on-chain settlement)'}`);
}
