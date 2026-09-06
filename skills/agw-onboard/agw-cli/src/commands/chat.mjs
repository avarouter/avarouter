// agw chat [model] [message]
//
// Hot-path call using the API key from state.
//
//   $ agw chat                          # default model + default message
//   $ agw chat gpt-4o-mini "hi"         # model + message
//   $ echo '{"messages":[...]}' | agw chat --stdin
//
// Returns the gateway response. Exits non-zero on balance exhaustion.

import { AGWClient } from '../client.mjs';
import { loadState } from '../state.mjs';

export const cmd = {
  name: 'chat',
  description: 'Make one hot-path call using the saved API key',
  usage: 'agw chat [model] [message]',
  flags: [
    { name: 'stdin',  type: 'bool',   desc: 'read request body from stdin (JSON)' },
    { name: 'model',  type: 'string', desc: 'model to call (default: mock-model)' },
    { name: 'key',    type: 'string', desc: 'API key (default: from state)' },
  ],
  examples: [
    'agw chat',
    'agw chat gpt-4o-mini "hello"',
    'echo \'{"messages":[{"role":"user","content":"hi"}]}\' | agw chat --stdin',
  ],
};

export async function run({ args, flags, cfg }) {
  const st = loadState();
  const apiKey = flags.key || st?.apiKey;
  if (!apiKey) {
    throw new Error('no API key — run `agw mint` first or pass --key agw_...');
  }
  const client = new AGWClient({ gateway: st?.gateway });

  let body;
  if (flags.stdin) {
    const buf = await new Promise((res, rej) => {
      let d = '';
      process.stdin.setEncoding('utf8');
      process.stdin.on('data', (c) => d += c);
      process.stdin.on('end', () => res(d));
      process.stdin.on('error', rej);
    });
    body = JSON.parse(buf);
  } else {
    const model = flags.model || args[0] || 'mock-model';
    const message = args[1] || args[0] || 'Hello from agw CLI';
    body = {
      model,
      messages: [{ role: 'user', content: message }],
    };
  }

  const r = await client.chat(apiKey, body);
  if (r.status === 402) {
    console.log('chat');
    console.log('  status=402 balance-exhausted');
    console.log(`  body: ${JSON.stringify(r.body)}`);
    process.exit(2);
  }
  console.log('chat');
  console.log(`  status: ${r.status}`);
  if (r.body?.choices) {
    const c = r.body.choices[0];
    console.log(`  reply:  ${c?.message?.content || JSON.stringify(c)}`);
  } else {
    console.log(`  body:   ${JSON.stringify(r.body)}`);
  }
  if (r.body?.usage) {
    const u = r.body.usage;
    console.log(`  tokens: in=${u.prompt_tokens} out=${u.completion_tokens} total=${u.total_tokens}`);
  }
}
