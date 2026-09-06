---
name: agw-onboard
description: Onboard a user to avarouter (the Avalanche Native AI Gateway at https://avarouter.up.railway.app) and hand them an API key. Use this skill when the user wants to use the avarouter x402 prepaid gateway, get an API key, check their balance, or set up an AI agent to call upstream LLMs through avarouter. The skill provides a single `agw` CLI with subcommands for the full flow: topup (EIP-3009), login (EIP-191 → session token), mint key, balance / sessions / key management, and hot-path chat.
---

# agw-onboard

Onboard a user to **avarouter** (the Avalanche Native AI Gateway) and hand them their first API key. The skill provides a single **`agw` CLI** with 14 subcommands, plus a programmatic `AGWClient` library for direct use.

```
$ agw flow 1000        # one-shot onboarding (topup + login + mint + chat)
$ agw chat "hello"     # use the saved API key
$ agw balance          # check balance
```

State (session token, API key, user identity) is auto-persisted to `~/.agw/state.json`, so the user runs each command independently — no need to re-sign or pass tokens.

## Production endpoint

Default: **`https://avarouter.up.railway.app`**. Override with `AGW_GATEWAY=...`, `.env`, or `~/.agw/config`.

## Demo key (Fuji testnet, no real value)

For quick demos, the skill ships a built-in Fuji testnet key. **DO NOT use on mainnet — it has no real funds.**

```bash
export AGW_PRIVATE_KEY=0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6
export AGW_GATEWAY=https://avarouter.up.railway.app   # or http://127.0.0.1:8080

agw flow 1000
```

- **address**: `0x90F79bf6EB2c4f870365E785982E1f101E93b906`
- **network**: Avalanche Fuji C-Chain (chain id 43113)
- **USDC**: `0x5425890298aed601595a70AB815c96711a31Bc65`
- **server mode**: offline (no `AGW_USDC_SERVER_KEY` set by default → topups credit the internal ledger without on-chain settlement)

## When to use this skill

Invoke when the user says any of:

- "给我弄个 avarouter 的 API key" / "I need an avarouter API key"
- "我想用 avarouter 跑 agent" / "I want to run my agent on avarouter"
- "Top up my avarouter balance" / "avarouter 充值"
- "avarouter 余额还剩多少" / "what's my avarouter balance"
- "列出 / 撤销 / 轮换我的 key"
- "登录 avarouter 管理界面"
- "在 avarouter.up.railway.app 上给我弄 key"
- "用 agw CLI 帮我……" / "use the agw CLI to …"
- "AGW" / "agw" (the previous project name — same gateway)

Do **not** invoke when:
- The user wants to use an existing API key (no onboarding needed)
- The user wants a different gateway (not avarouter)
- The user wants to set up the gateway server itself (operator flow)

## Quick reference — the 14 commands

```
$ agw help                  # full help
$ agw config                # show active gateway / network / USDC

# Onboarding (signing needed)
$ agw topup   <amount>      # x402 EIP-3009 (auto-register on first call)
$ agw login   [--ttl N]     # EIP-191 → session token
$ agw logout                # revoke session
$ agw mint                  # mint API key
$ agw rotate                # revoke all keys, issue new
$ agw revoke  <prefix>      # revoke single key

# Read (mostly no signing)
$ agw balance [--user 0x..] # balance (own / admin)
$ agw keys    [--user 0x..] # list API keys
$ agw sessions              # list active sessions
$ agw users    [--window]   # all users
$ agw whoami                # current user from state

# Hot path
$ agw chat [model] [msg]    # one chat call (uses saved key)
$ agw chat --stdin          # read JSON request from stdin

# Compound
$ agw flow   <amount>       # topup → login → mint → chat in one

# Utility
$ agw state [show|clear|path]
$ agw help [command]
```

## Quickstart

### Step 1 — Pick a wallet

Either the user's own wallet, or the built-in **demo key** for quick testing:

```bash
# Built-in demo key (Fuji testnet, no real value)
export AGW_PRIVATE_KEY=0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6

# Or the user's own
export AGW_PRIVATE_KEY=0x<user key hex>
```

### Step 2 — Run the flow

```bash
# One-shot onboarding (4 steps in one)
agw flow 1000

# Or step by step
agw topup 1000    # auto-registers
agw login          # → state.sessionToken
agw mint           # → state.apiKey
agw chat "hi"      # uses state.apiKey
```

### Step 3 — Hand the API key to the user

After `agw flow` or `agw mint`, the plaintext key is printed once. The user must save it. The agent should also tell the user about `~/.agw/state.json` so they can later run `agw chat` themselves.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `AGW_GATEWAY` | `https://avarouter.up.railway.app` | base URL of the avarouter server |
| `AGW_NETWORK` | `fuji-testnet` | network name (informational) |
| `AGW_CHAIN_ID` | `43113` | numeric chain id |
| `AGW_USDC_ADDRESS` | Fuji USDC | USDC contract on the chain |
| `AGW_PRIVATE_KEY` | (dev seed) | signing wallet (or `AGW_PK`, `<NAME>_PK`) |
| `AGW_KEYSTORE` + `AGW_KEYSTORE_PASSWORD` | | encrypted JSON keystore |
| `AGW_MNEMONIC` | | BIP-39 mnemonic |
| `AGW_STATE_DIR` | `~/.agw` | where `state.json` is stored |
| `AGW_DEBUG` | | if set, prints stack traces on error |

Priority: shell env > `.env` (CWD) > `~/.agw/config` > built-in default.

To run against a different gateway:

```bash
# Inline
AGW_GATEWAY=http://localhost:8080 agw flow 1000

# Via .env file
cat > .env <<EOF
AGW_GATEWAY=https://staging.example.com
AGW_PRIVATE_KEY=0x...
EOF
agw flow 1000

# Programmatically
import { AGWClient } from './agw-cli/src/client.mjs';
const client = new AGWClient({ gateway: 'http://localhost:8080' });
```

## Endpoints (full reference)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/v1/topup` | per-req EIP-191 + EIP-3009 | Top up. First call auto-registers. |
| POST | `/v1/auth` | per-req EIP-191 | Sign in, get a session token. |
| POST | `/v1/auth/logout` | `X-AGW-Session` | Invalidate a session. |
| GET  | `/v1/auth/sessions` | session or sig | List the user's active sessions. |
| POST | `/v1/keys` | session or sig | Mint a new API key. Plaintext returned ONCE. |
| GET  | `/v1/keys` | session or sig | List the user's keys. |
| POST | `/v1/keys/rotate` | session or sig | Revoke all of the user's keys, issue new. |
| DELETE | `/v1/keys/{prefix}` | session or sig | Revoke a single key. |
| GET  | `/payments/balance` | session, sig, `?user=`, or open | **Balance query** |
| GET  | `/payments/users` | open | All users + balances + per-user stats |
| GET  | `/payments/keys?user=...` | open | Per-key rollups |
| POST | `/v1/chat/completions` | `Authorization: Bearer <key>` | Hot-path LLM call. Returns 402 if balance ≤ 0. |

## Library API (`agw-cli/src/client.mjs`)

For programmatic use without the CLI:

```js
import { AGWClient, loadWallet } from './agw-cli/src/client.mjs';
import { loadWallet as _w } from './agw-cli/src/wallet.mjs';

const client = new AGWClient();                          // env / .env / Railway default
const wallet = _w({ name: 'alice' });                    // auto-loads AGW_PRIVATE_KEY

// Onboarding
const t = await client.topup({ wallet, amount: '1000' });
const l = await client.login(wallet);
const k = await client.mintKey({ sessionToken: l.sessionToken });

// Read
const bal = await client.balance({ user: t.user });
const keys = await client.listKeys({ sessionToken: l.sessionToken });
const users = await client.listUsers({ window: '24h' });

// Hot path
const reply = await client.chat(k.key, {
  model: 'gpt-4o-mini',
  messages: [{ role: 'user', content: 'hi' }],
});
```

## How the agent should drive this

1. **Confirm intent**: the user wants an AGW key. Default gateway is `https://avarouter.up.railway.app`.
2. **Get the wallet**: ask the user for a private key (or wallet connect). The agent MUST NOT reuse its own wallet.
3. **Run `agw flow <amount>`** (default 1000 micro-USDC for a demo). This signs 3 times (EIP-3009 challenge, signed retry, EIP-191 login) and returns the API key.
4. **Hand the key to the user**. Print the key and `~/.agw/state.json` location.
5. **Optionally verify** with `agw balance` or `agw chat "hi"`.

If the user already has a key, skip onboarding and just run `agw chat` with `--key agw_...`.

## State file

State is persisted to `~/.agw/state.json`:

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

- `agw state`         — show path + summary
- `agw state path`    — print path only
- `agw state clear`   — wipe the state (will require `agw login` + `agw mint` again)
- `AGW_STATE_DIR=/foo` — override location

## Failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| `signature does not match X-Payer` | Wallet signer ≠ claimed X-Payer | Re-fetch the wallet / re-sign |
| `X-AGW-Timestamp out of window` | Client clock skew > 5 min | Sync clocks |
| `X-AGW-Session expired` | Idle 30m or absolute 24h exceeded | `agw login` |
| `balance exhausted` (402) | Hot path balance ≤ 0 | `agw topup 1000` |
| `signature verification failed: authorization expired` | x402 challenge (5min) expired | Re-run `agw topup` |
| `no active key with that prefix for this user` | Revoking another user's key | Verify the prefix belongs to the right user |
| `fetch failed: ECONNREFUSED 127.0.0.1:8080` | Wrong gateway | Set `AGW_GATEWAY=...` |
| `no API key — run agw mint first` | State was cleared | `agw mint` |

## Files in this skill

```
SKILL.md               this file
lib.mjs                backwards-compat re-export of AGWClient
package.json           dependencies (ethers v6) + npm script shortcuts
README.md              human-readable quickstart
examples/              12 runnable wrappers around the CLI
  .env.example         .env template
  _cli.mjs             shell-out helper
  01-topup.mjs         → agw topup
  02-login.mjs         → agw login
  03-mint-key.mjs      → agw mint
  04-list-keys.mjs     → agw keys
  05-check-balance.mjs → agw balance
  06-rotate-key.mjs    → agw rotate
  07-revoke-key.mjs    → agw revoke
  08-logout.mjs        → agw logout
  09-list-users.mjs    → agw users
  10-list-sessions.mjs → agw sessions
  11-chat.mjs          → agw chat
  99-full-flow.mjs     → agw flow
agw-cli/               the actual CLI
  bin/agw.mjs          entry point
  src/                 modules
  package.json         bin: { agw: "./bin/agw.mjs" }
  README.md            CLI docs
node_modules -> symlink to the demo's node_modules
```

The CLI source of truth lives at
`/workspace/agw-x402-report/demo/node/agw-cli/`.
This SKILL vendors a copy of the same files; both are kept in sync.
