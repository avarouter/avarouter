# agw — unified CLI for the AGW (x402 prepaid LLM gateway)

A single `agw` command that talks to the AGW HTTP API. The CLI auto-saves
session tokens, API keys, and user identity to `~/.agw/state.json` so you
don't have to pass them around between commands.

## Install (global)

From the demo directory:

```bash
cd /workspace/agw-x402-report/demo/node
npm install -g agw-cli   # not yet published; or
npm link ./agw-cli
```

Or run directly:

```bash
node /workspace/agw-x402-report/demo/node/agw-cli/bin/agw.mjs <command>
```

## Quickstart (3 commands)

```bash
# 1. Show config (one-shot setup check)
agw config

# 2. One-shot onboarding: topup → login → mint → chat
AGW_PRIVATE_KEY=0x... agw flow 1000

# 3. Use the saved key
agw chat "hi"
agw balance
```

## Commands

```
onboarding:
  topup <amount>             # EIP-3009 x402 (auto-register on first call)
  login                      # EIP-191 → session token (saved to state)
  logout                     # revoke current session
  mint                       # mint API key (saved to state)
  rotate                     # revoke all keys, issue new one
  revoke <prefix>            # revoke a single key by prefix

read:
  balance [--user 0x...]     # balance (admin / own-via-session / own-via-sig)
  keys    [--user 0x...]     # list API keys
  sessions                   # list active sessions
  users    [--window 1h|24h|7d|30d|all]   # all registered users
  whoami                     # show current user identity (from state)

hot path:
  chat [model] [message]     # use the saved API key
  chat --stdin               # read JSON request body from stdin

compound:
  flow <amount>              # topup → login → mint → chat (in one)

utility:
  config                     # show active configuration
  state [show|clear|path]    # inspect/clear the saved state file
  help [command]             # show help
```

Run `agw help <command>` for full flag info.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `AGW_GATEWAY` | `https://avarouter.up.railway.app` | base URL of the AGW server |
| `AGW_NETWORK` | `fuji-testnet` | `fuji-testnet` / `mainnet` |
| `AGW_CHAIN_ID` | `43113` | numeric chain id |
| `AGW_USDC_ADDRESS` | Fuji USDC | USDC contract on the chain |
| `AGW_PRIVATE_KEY` | (dev seed) | signing wallet (or `AGW_PK` or `<name>_PK`) |
| `AGW_KEYSTORE` + `AGW_KEYSTORE_PASSWORD` | | encrypted JSON keystore |
| `AGW_MNEMONIC` | | BIP-39 mnemonic |
| `AGW_STATE_DIR` | `~/.agw` | where `state.json` is stored |
| `AGW_DEBUG` | | if set, prints stack traces on error |

Resolution priority (highest first): shell env → `.env` in CWD → `~/.agw/config` → built-in default.

## State file

State is stored at `~/.agw/state.json` (or `$AGW_STATE_DIR/state.json`):

```json
{
  "gateway":          "https://avarouter.up.railway.app",
  "user":             "0x...",
  "payTo":            "0x...",
  "sessionToken":     "ags_...",
  "sessionExpiresAt": 1700000000,
  "apiKey":           "agw_...",
  "apiKeyPrefix":     "agw_dPak",
  "apiKeyCreatedAt":  1700000000,
  "lastBalance":      "1000",
  "updatedAt":        "2026-09-06T08:00:00.000Z"
}
```

To inspect: `agw state` or `agw state show`.
To clear: `agw state clear` (will require `agw login` and `agw mint` again before next use).
The plaintext `sessionToken` and `apiKey` are stored in cleartext. Do not share the state file.

## Library

For programmatic use, see `src/client.mjs`:

```js
import { AGWClient } from './src/client.mjs';
const client = new AGWClient();
const r = await client.balance({ user: '0x...' });
console.log(r.balance);
```

## Files

```
agw-cli/
├── package.json            bin: { agw: "./bin/agw.mjs" }
├── bin/
│   └── agw.mjs             CLI entry
├── src/
│   ├── config.mjs          env / gateway / USDC resolution
│   ├── wallet.mjs          loadWallet
│   ├── crypto.mjs          EIP-191 + EIP-3009 signing
│   ├── client.mjs          AGWClient (HTTP)
│   ├── state.mjs           state.json management
│   └── commands/           one file per subcommand
│       ├── topup.mjs
│       ├── login.mjs
│       ├── logout.mjs
│       ├── mint.mjs
│       ├── rotate.mjs
│       ├── revoke.mjs
│       ├── balance.mjs
│       ├── keys.mjs
│       ├── sessions.mjs
│       ├── users.mjs
│       ├── whoami.mjs
│       ├── chat.mjs
│       ├── flow.mjs
│       ├── config.mjs
│       └── state.mjs
└── README.md
```
