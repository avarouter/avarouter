// agw whoami
//
// Show the current user identity (from state).

import { loadState, maskToken } from '../state.mjs';

export const cmd = {
  name: 'whoami',
  description: 'Show the current user identity (from state)',
  usage: 'agw whoami',
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  if (!st) {
    console.log('whoami');
    console.log('  (no state — run `agw topup 1000 && agw login && agw mint` to set up)');
    return;
  }
  console.log('whoami');
  console.log(`  gateway:    ${st.gateway || '(unset)'}`);
  console.log(`  user:       ${st.user || '(unset)'}`);
  console.log(`  payTo:      ${st.payTo || st.user || '(unset)'}`);
  console.log(`  session:    ${st.sessionToken ? maskToken(st.sessionToken) : '(none — run `agw login`)'}`);
  if (st.sessionExpiresAt) {
    console.log(`    expires:  ${new Date(st.sessionExpiresAt * 1000).toISOString()}`);
  }
  console.log(`  api key:    ${st.apiKey ? maskToken(st.apiKey, 12) : '(none — run `agw mint`)'}`);
  if (st.apiKeyCreatedAt) {
    console.log(`    created:  ${new Date(st.apiKeyCreatedAt * 1000).toISOString()}`);
  }
  if (st.lastBalance) {
    console.log(`  balance:    ${st.lastBalance} micro-USDC`);
  }
  console.log(`  updatedAt:  ${st.updatedAt}`);
}
