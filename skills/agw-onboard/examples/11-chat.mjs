// 11-chat.mjs
//
// Make one hot-path call using an API key. The gateway debits
// the key-owner's balance on success. Returns 402 if balance=0.
//
// Usage:
//   AGW_API_KEY=agw_... node examples/11-chat.mjs [model]
//   AGW_API_KEY=agw_... node examples/11-chat.mjs mock-model
//   AGW_API_KEY=agw_... node examples/11-chat.mjs gpt-4o-mini

import { AGWClient } from './lib.mjs';

const apiKey = process.env.AGW_API_KEY;
const model  = process.argv[2] || 'mock-model';

if (!apiKey) {
  console.error('need AGW_API_KEY');
  process.exit(1);
}

const client = new AGWClient();
const r = await client.chat(apiKey, {
  model,
  messages: [{ role: 'user', content: 'hello' }],
});

if (r.status === 402) {
  console.log('chat → 402 (balance exhausted)');
  console.log('  reason:', r.body?.reason);
  process.exit(2);
}

console.log('chat → 200');
const msg = r.body?.choices?.[0]?.message;
console.log('  model:   ', r.body?.model);
console.log('  reply:   ', msg?.content?.slice(0, 80));
const usage = r.body?.usage;
if (usage) {
  console.log('  tokens:  in=' + usage.prompt_tokens + '  out=' + usage.completion_tokens + '  total=' + usage.total_tokens);
}
