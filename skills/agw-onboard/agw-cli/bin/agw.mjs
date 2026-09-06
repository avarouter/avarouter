#!/usr/bin/env node
// agw — CLI for avarouter (the Avalanche Native AI Gateway).
//
// Usage:
//   agw <command> [args] [--flags]
//
// Commands:
//   topup, login, logout, mint, rotate, revoke,
//   balance, keys, sessions, users, whoami,
//   chat, flow, config, state, help
//
// Config:
//   AGW_GATEWAY        base URL (default: https://avarouter.up.railway.app)
//   AGW_NETWORK        fuji-testnet | mainnet
//   AGW_CHAIN_ID       numeric chain id
//   AGW_USDC_ADDRESS   USDC contract on the chain
//   AGW_PRIVATE_KEY    signing wallet (or AGW_PK, or <name>_PK)
//   AGW_KEYSTORE       encrypted JSON keystore path
//   AGW_KEYSTORE_PASSWORD
//   AGW_MNEMONIC       BIP-39 mnemonic
//   AGW_STATE_DIR      state file location (default: ~/.agw)

import { describeConfig } from '../src/config.mjs';

import * as topup    from '../src/commands/topup.mjs';
import * as login    from '../src/commands/login.mjs';
import * as logout   from '../src/commands/logout.mjs';
import * as mint     from '../src/commands/mint.mjs';
import * as rotate   from '../src/commands/rotate.mjs';
import * as revoke   from '../src/commands/revoke.mjs';
import * as balance  from '../src/commands/balance.mjs';
import * as keys     from '../src/commands/keys.mjs';
import * as sessions from '../src/commands/sessions.mjs';
import * as users    from '../src/commands/users.mjs';
import * as whoami   from '../src/commands/whoami.mjs';
import * as chat     from '../src/commands/chat.mjs';
import * as flow     from '../src/commands/flow.mjs';
import * as cfg      from '../src/commands/config.mjs';
import * as st       from '../src/commands/state.mjs';

const COMMANDS = {
  topup, login, logout, mint, rotate, revoke,
  balance, keys, sessions, users, whoami,
  chat, flow, config: cfg, state: st,
  help: { cmd: { name: 'help', description: 'Show help' }, run: () => printHelp() },
};

function printHelp(cmdName) {
  if (cmdName) {
    const c = COMMANDS[cmdName];
    if (!c) { console.log(`unknown command: ${cmdName}`); printHelp(); process.exit(1); }
    console.log(`agw ${c.cmd.name} — ${c.cmd.description}`);
    console.log();
    console.log(`  usage: ${c.cmd.usage}`);
    if (c.cmd.flags?.length) {
      console.log();
      console.log('  flags:');
      for (const f of c.cmd.flags) {
        const v = f.type === 'bool' ? '' : ' <value>';
        const d = f.default !== undefined ? ` (default: ${f.default})` : '';
        console.log(`    --${f.name}${v}${d}  ${f.desc}`);
      }
    }
    if (c.cmd.examples?.length) {
      console.log();
      console.log('  examples:');
      for (const ex of c.cmd.examples) console.log(`    $ ${ex}`);
    }
    return;
  }
  console.log('agw — avarouter (Avalanche Native AI Gateway) CLI');
  console.log();
  console.log('commands:');
  const groups = [
    ['onboarding', ['topup', 'login', 'logout', 'mint', 'rotate', 'revoke']],
    ['read',       ['balance', 'keys', 'sessions', 'users', 'whoami']],
    ['hot path',   ['chat']],
    ['compound',   ['flow']],
    ['utility',    ['config', 'state', 'help']],
  ];
  for (const [name, list] of groups) {
    console.log(`  ${name}:`);
    for (const c of list) {
      const cc = COMMANDS[c];
      console.log(`    ${c.padEnd(10)} ${cc.cmd.description}`);
    }
  }
  console.log();
  console.log('  $ agw help <command>   # detailed help for a command');
  console.log();
  console.log('quickstart:');
  console.log('  $ agw flow 1000         # one-shot onboarding (topup+login+mint+chat)');
  console.log('  $ agw chat "hello"      # use the saved key');
  console.log('  $ agw balance           # check balance');
  console.log();
  console.log('config (env vars):');
  console.log('  AGW_GATEWAY     base URL (default: https://avarouter.up.railway.app)');
  console.log('  AGW_PRIVATE_KEY signing wallet');
  console.log('  AGW_STATE_DIR   state file location (default: ~/.agw)');
}

function parseArgs(argv) {
  const args = [];
  const flags = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--help' || a === '-h') {
      flags.help = true;
    } else if (a.startsWith('--')) {
      const eq = a.indexOf('=');
      if (eq !== -1) {
        const k = a.slice(2, eq);
        const v = a.slice(eq + 1);
        flags[k] = v;
      } else {
        const k = a.slice(2);
        const next = argv[i + 1];
        if (next !== undefined && !next.startsWith('--')) {
          flags[k] = next;
          i++;
        } else {
          flags[k] = true;
        }
      }
    } else {
      args.push(a);
    }
  }
  return { args, flags };
}

async function main() {
  const argv = process.argv.slice(2);
  const [cmdName, ...rest] = argv;
  if (!cmdName || cmdName === 'help' && rest.length === 0) {
    printHelp();
    return;
  }
  if (cmdName === 'help') {
    printHelp(rest[0]);
    return;
  }
  if (cmdName === '--help' || cmdName === '-h') {
    printHelp();
    return;
  }
  const c = COMMANDS[cmdName];
  if (!c) {
    console.error(`agw: unknown command '${cmdName}'\n`);
    console.error(`Run \`agw help\` for a list of commands.`);
    process.exit(1);
  }
  const { args, flags } = parseArgs(rest);
  if (flags.help) {
    printHelp(cmdName);
    return;
  }
  const cfgSnapshot = describeConfig();
  try {
    await c.run({ args, flags, cfg: cfgSnapshot });
  } catch (e) {
    console.error(`\nagw ${cmdName}: ${e.message || e}`);
    if (process.env.AGW_DEBUG) console.error(e.stack);
    process.exit(1);
  }
}

main();
