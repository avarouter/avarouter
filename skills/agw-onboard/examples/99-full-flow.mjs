// 99-full-flow.mjs
//
// End-to-end user onboarding — what an Agent runs to give a
// fresh user their first API key:
//
//   1. topup       (auto-registers the user)
//   2. login       (one-time EIP-191 sig → session token)
//   3. mint-key    (uses session, no signing)
//   4. list-keys   (verify the new key is there)
//   5. chat        (use the new key on the hot path)
//   6. logout      (close the session)
//
// Usage:
//   AGW_PRIVATE_KEY=0x... node examples/99-full-flow.mjs [amountMicro]
//   AGW_PRIVATE_KEY=0x... AGW_GATEWAY=https://avarouter.up.railway.app node examples/99-full-flow.mjs 1000
//   # or with a .env file:
//   AGW_PRIVATE_KEY=0x... node examples/99-full-flow.mjs
//
// .env file format (auto-loaded from CWD):
//   AGW_GATEWAY=https://avarouter.up.railway.app
//   AGW_NETWORK=fuji-testnet
//   AGW_CHAIN_ID=43113
//   AGW_USDC_ADDRESS=0x5425890298aed601595a70AB815c96711a31Bc65
//   AGW_PRIVATE_KEY=0x...

import { AGWClient, loadWallet, describeWallet } from './lib.mjs';

const amount = process.argv[2] || '1000';

const wallet = loadWallet({ name: 'alice' });
const desc   = describeWallet(wallet);
const client = new AGWClient();

console.log('=== full-flow ===');
const cfg = client.describe();
console.log(`  gateway: ${cfg.gateway}`);
console.log(`  network: ${cfg.network} (chainId=${cfg.chainId})`);
console.log(`  usdc:    ${cfg.usdcAddress}`);
console.log();

// 1. topup
console.log('[1/6] topup');
const top = await client.topup({ wallet, amount });
console.log(`      user=${top.user}`);
console.log(`      payTo=${top.payTo}`);
console.log(`      balance=${top.balance}\n`);

// 2. login
console.log('[2/6] login');
const session = await client.login(wallet, { ttlSeconds: 3600 });
console.log(`      token=${session.sessionToken.slice(0, 20)}…`);
console.log(`      expires=${new Date(session.expiresAt * 1000).toISOString()}\n`);

// 3. mint-key
console.log('[3/6] mint-key');
const minted = await client.mintKey(session.sessionToken);
console.log(`      key=${minted.key}`);
console.log(`      ⚠️  ${minted.warning}\n`);

// 4. list-keys
console.log('[4/6] list-keys');
const keys = await client.listKeys({ sessionToken: session.sessionToken });
console.log(`      ${keys.count} keys:`);
for (const k of keys.items) {
  console.log(`        ${k.prefix}  ${k.active ? 'active' : 'revoked'}`);
}
console.log();

// 5. chat
console.log('[5/6] chat (using the new key)');
const chat = await client.chat(minted.key, {
  model: 'mock-model',
  messages: [{ role: 'user', content: 'hello from the agent' }],
});
if (chat.status === 200) {
  console.log(`      200 OK — ${chat.body?.choices?.[0]?.message?.content}`);
  console.log(`      tokens: in=${chat.body?.usage?.prompt_tokens}  out=${chat.body?.usage?.completion_tokens}`);
} else {
  console.log(`      ${chat.status} ${chat.text}`);
}
console.log();

// 6. logout
console.log('[6/6] logout');
const out = await client.logout(session.sessionToken);
console.log(`      ok=${out.json?.ok}  revoked=${out.json?.revoked}`);
console.log();

console.log('=== done ===');
console.log(`User:     ${desc.addressLower}`);
console.log(`API key:  ${minted.key}`);
console.log(`PayTo:    ${top.payTo}`);
