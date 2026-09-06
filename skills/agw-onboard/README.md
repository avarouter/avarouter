# avarouter

**Avalanche Native AI Gateway** — a production-grade x402 prepaid LLM routing gateway on Avalanche C-Chain. Multi-tenant balance, per-key hot-path metering, SIWE-style session tokens, and an embedded Mavis skill so any agent can onboard in three commands.

```
$ agw flow 1000
=== full flow ===
  gateway: https://avarouter.up.railway.app
  user:    0x90F79bf6EB2c4f870365E785982E1f101E93b906
[1/5] topup   … ok (balance=1000)
[2/5] login   … ok (token=ags_…)
[3/5] mint    … ok (key=agw_…)
[4/5] chat    … ok (200 — "hi")
```

---

## Why avarouter

Traditional LLM API gateways charge you on a credit card and require an account. **avarouter** charges you in **USDC on Avalanche** — top up, get an API key, every request debits your on-chain balance at micro-USDC granularity.

- **No accounts, no KYC.** Bring any EVM wallet.
- **No per-call on-chain tx.** Topup settles on-chain; the hot path debits an internal ledger.
- **Multi-tenant by default.** Each user picks their own deposit address.
- **Cryptographic bearer tokens.** API keys never stored in cleartext; sessions expire.
- **Single binary, single file ledger.** `wallet.jsonl` is the entire state.

---

## Architecture (one page)

```
┌──────────────────────────────────────────────────────────┐
│  user (any EVM wallet)                                   │
│      │                                                   │
│      │  1. POST /v1/topup                                │
│      │     body: EIP-3009 TransferWithAuthorization      │
│      │     hdrs:  EIP-191 management signature           │
│      ▼                                                   │
│  ┌────────────────────────────────────────────┐          │
│  │  avarouter  (Go single binary)             │          │
│  │                                            │          │
│  │  · x402 challenge (402 + EIP-3009 params)  │          │
│  │  · signature verify (off-chain)            │          │
│  │  · on-chain settle (if ServerKey set)      │──> USDC contract
│  │  · credit multi-tenant Wallet              │     (Fuji 0x5425890298aed…)
│  │                                            │          │
│  │  · /v1/auth       EIP-191 → session token  │          │
│  │  · /v1/keys       mint agw_<32B> key       │          │
│  │  · /payments/…    balance / users / keys   │          │
│  │  · /v1/chat/…     hot path (Bearer key)    │──> upstream LLM
│  │                                            │          │
│  │  State:                                      │          │
│  │    wallet.jsonl   append-only event log     │          │
│  │    in-memory      map[from]*UserAccount     │          │
│  │                   map[sha256(key)]*KeyRec   │          │
│  │                   SessionStore (TTL+idle)   │          │
│  └────────────────────────────────────────────┘          │
└──────────────────────────────────────────────────────────┘
```

---

## Key technical points

### Cryptography

| Layer | Algorithm | What it signs |
|---|---|---|
| Topup authorization | **EIP-3009** (USDC `TransferWithAuthorization`) | "I authorize the gateway to debit N USDC from my wallet" |
| Management calls | **EIP-191** + length-prefix (`\x19Ethereum Signed Message:\n` + len) | The canonical request: method, path, body-sha256, timestamp, payer |
| Auth (login) | EIP-191 | One-time `from + expiresAt + timestamp + payer` |
| Session token | 32 random bytes, base64url | Server stores only `sha256(token)`; plaintext returned once |

### Hot path vs cold path

```
Cold path (topup)              Hot path (chat)
─────────────────              ───────────────
EIP-3009 signed    ───>        Bearer <apiKey>    ───>
EIP-191 mgmt sig   ───>        (no signing)       ───>
ServerKey submit   ───>        Internal ledger    ───>
  on-chain tx                  micro-USDC debit
(offline mode: skip)           (no RPC, instant)
```

The hot path never touches the chain. The only on-chain call is the topup.

### Multi-tenant model

```
users[from]    UserAccount  (created on first topup)
keys[hash]     KeyRecord    (Owner: from)
sessions[id]   Session      (User: from)
```

`payTo` defaults to `from` but can be any address the user controls. Useful for routing topups to a treasury while signing with a hot wallet.

### Session token (SIWE-style)

| Property | Value |
|---|---|
| Format | `ags_` + 43 base64url chars (32 bytes) |
| Stored as | `sha256(token)` hex |
| Absolute lifetime | 24 hours |
| Idle timeout | 30 minutes (bumped on use) |
| Replay window | 5 minutes (per-req EIP-191) |

### x402 message formats (signed canonical strings)

**Management signature** (EIP-191, every protected request):
```
AGW-MANAGE-v1
method=POST
path=/v1/keys
body-sha256=0x<hex>
timestamp=<unix sec>
payer=0x...
```

**Auth signature** (EIP-191, one-time at login):
```
AGW-AUTH-v1
from=0x...
expiresAt=<unix sec>
timestamp=<unix sec>
payer=0x...
```

---

## Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/v1/topup` | EIP-191 + EIP-3009 | Top up. First call auto-registers the user. Sessions rejected. |
| POST | `/v1/auth` | EIP-191 | Sign in, get a session token. |
| GET  | `/v1/auth/sessions` | session or sig | List the user's active sessions. |
| POST | `/v1/auth/logout` | session | Invalidate a session. |
| POST | `/v1/keys` | session or sig | Mint an API key. Plaintext returned ONCE. |
| GET  | `/v1/keys` | session or sig | List the user's keys. |
| POST | `/v1/keys/rotate` | session or sig | Revoke all keys, issue a new one. |
| DELETE | `/v1/keys/{prefix}` | session or sig | Revoke a single key. |
| GET  | `/payments/balance` | session / X-Payer / `?user=` / open | Balance lookup (3 modes + aggregate). |
| GET  | `/payments/users` | open | All users + per-user stats. |
| GET  | `/payments/keys?user=` | open | Per-key rollups. |
| POST | `/v1/chat/completions` | `Authorization: Bearer <key>` | Hot-path LLM call. Returns 402 if balance ≤ 0. |

---

## Try it in 30 seconds

### Option A — use the built-in demo key (Fuji testnet, **no real value**)

```bash
export AGW_PRIVATE_KEY=0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6
export AGW_GATEWAY=https://avarouter.up.railway.app    # or http://127.0.0.1:8080 for local

agw flow 1000
```

That single command:
1. Registers you (first call only)
2. Topups 1000 micro-USDC
3. Signs you in (session token)
4. Mints an API key
5. Makes one chat call

Save the printed `agw_…` key. Then:

```bash
agw chat "hello"           # uses saved key
agw balance                # uses saved session
agw keys                   # list your keys
```

> The demo key address is `0x90F79bf6EB2c4f870365E785982E1f101E93b906`. It is for **Fuji testnet only** and has **no real funds**. Server runs in **offline mode** by default (no on-chain settlement).

### Option B — your own wallet (any chain)

```bash
export AGW_PRIVATE_KEY=0x<your key hex>
export AGW_GATEWAY=https://avarouter.up.railway.app
agw flow 1000
```

For real on-chain settlement, set `AGW_USDC_SERVER_KEY` on the **server side** (see [Running your own server](#running-your-own-server)).

### Option C — local server (dev)

```bash
# 1. Build
go build -o avarouter ./cmd/agw

# 2. Start with Fuji defaults (no ServerKey = offline mode)
AGW_USDC_RPC=https://api.avax-test.network/ext/bc/C/rpc \
AGW_USDC_ADDRESS=0x5425890298aed601595a70AB815c96711a31Bc65 \
AGW_USDC_CHAIN_ID=43113 \
  ./avarouter --config config.example.yaml --listen :8080 --data-dir ./data

# 3. Use the CLI against your local server
AGW_GATEWAY=http://127.0.0.1:8080 \
AGW_PRIVATE_KEY=0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6 \
  agw flow 1000
```

---

## Running your own server

Server-side env vars (read once at startup):

| Var | Required | Default | Purpose |
|---|---|---|---|
| `AGW_USDC_RPC` | yes | (none) | JSON-RPC URL of the chain (e.g. `https://api.avax-test.network/ext/bc/C/rpc`) |
| `AGW_USDC_ADDRESS` | yes | (none) | USDC contract on that chain |
| `AGW_USDC_CHAIN_ID` | yes | (none) | Numeric chain id (`43113` Fuji, `1` mainnet) |
| `AGW_USDC_SERVER_KEY` | no | (offline) | Path to a hex ECDSA private key OR a 64-char hex string. Hot wallet that submits `transferWithAuthorization` on the user's behalf. **If unset → offline mode (signature verified, no on-chain settlement).** |
| `AGW_USDC_SERVER_KEY_PATH` | no | — | Same, but only file path (preferred for prod). |
| `AGW_PAY_TO` | no | unset | If set, all topups are forced to this `payTo` (single-tenant mode). **Omit for multi-tenant.** |

Server flags:

```
--config <yaml>      config file (see config.example.yaml)
--listen :8080       listen address
--data-dir <path>    state dir (writes wallet.jsonl)
-log-stderr          log to stderr
```

### Deploy to Railway

```bash
# railway.json or Dockerfile is in the repo root
railway up
```

Railway env vars to set:
- `AGW_USDC_RPC` = `https://api.avax-test.network/ext/bc/C/rpc`
- `AGW_USDC_ADDRESS` = `0x5425890298aed601595a70AB815c96711a31Bc65`
- `AGW_USDC_CHAIN_ID` = `43113`
- `AGW_USDC_SERVER_KEY` = (only when you want on-chain settlement)
- `PORT` = `8080` (Railway sets this)

---

## Configuring the Mavis SKILL

The skill (`agw-onboard`) reads these from the environment:

| Env var | Default | Purpose |
|---|---|---|
| `AGW_GATEWAY` | `https://avarouter.up.railway.app` | base URL of the gateway |
| `AGW_NETWORK` | `fuji-testnet` | display name |
| `AGW_CHAIN_ID` | `43113` | numeric chain id |
| `AGW_USDC_ADDRESS` | Fuji USDC | USDC contract |
| `AGW_PRIVATE_KEY` | (dev seed) | signing wallet (or `AGW_PK`, `<NAME>_PK`) |
| `AGW_KEYSTORE` + `AGW_KEYSTORE_PASSWORD` | — | encrypted JSON keystore |
| `AGW_MNEMONIC` | — | BIP-39 mnemonic |
| `AGW_STATE_DIR` | `~/.agw` | where `state.json` is persisted |
| `AGW_DEBUG` | — | if set, prints stack traces on error |

Priority: shell env > `.env` in CWD > `~/.agw/config` > built-in default.

### To point the skill at a different gateway

```bash
# 1. Inline
AGW_GATEWAY=https://my-avarouter.example.com agw flow 1000

# 2. .env file
cat > .env <<EOF
AGW_GATEWAY=https://my-avarouter.example.com
AGW_PRIVATE_KEY=0x...
EOF
agw flow 1000

# 3. Constructor (programmatic)
import { AGWClient } from './agw-cli/src/client.mjs';
const c = new AGWClient({ gateway: 'http://localhost:8080' });
```

### State persistence

After each command, the skill writes `~/.agw/state.json`:

```json
{
  "gateway": "https://avarouter.up.railway.app",
  "user": "0x90F79bf6EB2c4f870365E785982E1f101E93b906",
  "payTo": "0x90F79bf6EB2c4f870365E785982E1f101E93b906",
  "sessionToken": "ags_…",
  "sessionExpiresAt": 1700000000,
  "apiKey": "agw_…",
  "apiKeyPrefix": "agw_dPak",
  "apiKeyCreatedAt": 1700000000,
  "lastBalance": "1000",
  "updatedAt": "2026-09-06T08:00:00Z"
}
```

This means each CLI call is independent — no need to re-sign or pass tokens.

- `agw state`         — show path + summary
- `agw state path`    — print the path
- `agw state clear`   — wipe the state (will require `agw login` + `agw mint` again)
- `AGW_STATE_DIR=/foo` — override location

### The 14 subcommands

```
onboarding:    topup, login, logout, mint, rotate, revoke
read:          balance, keys, sessions, users, whoami
hot path:      chat
compound:      flow
utility:       config, state, help
```

Run `agw help` for the full reference, or `agw help <command>` for one command.

---

## Why these design choices

| Choice | Alternative considered | Why this one wins |
|---|---|---|
| **Append-only `wallet.jsonl`** | SQLite / Postgres | One file = one backup = one git diff. Replay on startup is fast (linear scan). |
| **API keys + session tokens, two paths** | OAuth2 with refresh | Simpler, no client library needed, defense-in-depth (session OR sig) |
| **Per-req EIP-191 sig for management** | HMAC + shared secret | Works without shared state; user controls their own key |
| **Off-chain ledger + per-call debit** | Per-call on-chain transfer | Sub-millisecond hot path; no AVAX gas per call |
| **`payTo` decoupled from `from`** | `payTo = from` always | Lets users route to a treasury while signing with a hot wallet |
| **EIP-3009, not EIP-2612** | Permit + transferFrom | One signature from the user; the gateway submits. Cleaner UX. |
| **sha256(token) on server** | AES at rest | A log leak doesn't yield usable tokens. Plaintext returned once. |

---

## Project layout (in the skill directory)

```
SKILL.md                  loaded by Mavis; describes the skill
package.json              bin: { agw: "./agw-cli/bin/agw.mjs" }
agw-cli/                  the actual CLI
  bin/agw.mjs             entry point
  src/                    modules (config, wallet, crypto, client, state)
  src/commands/           one file per subcommand (14 total)
  test/test.mjs           smoke test (23 assertions)
  README.md               CLI docs
  package.json
examples/                 thin wrappers around `agw` for backward compat
  .env.example
  01-topup.mjs … 99-full-flow.mjs
node_modules -> symlink to the demo's node_modules
```

The CLI source of truth lives in the upstream avarouter repo at
`/workspace/agw-x402-report/demo/node/agw-cli/`. This SKILL vendors a copy.

---

## License

MIT-style, see the upstream avarouter repository.
