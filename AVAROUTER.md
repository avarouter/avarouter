# avarouter — Avalanche Native AI Gateway

Production-grade x402 prepaid LLM routing on Avalanche C-Chain. Multi-tenant balance, per-key hot-path metering, SIWE-style session tokens, an embedded Mavis skill, and a single `agw` CLI for onboarding.

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

## Why avarouter

Traditional LLM API gateways charge you on a credit card and require an account. **avarouter** charges you in **USDC on Avalanche** — top up, get an API key, every request debits your on-chain balance at micro-USDC granularity.

- **No accounts, no KYC.** Bring any EVM wallet.
- **No per-call on-chain tx.** Topup settles on-chain; the hot path debits an internal ledger.
- **Multi-tenant by default.** Each user picks their own deposit address.
- **Cryptographic bearer tokens.** API keys never stored in cleartext; sessions expire.
- **Single binary, single file ledger.** `wallet.jsonl` is the entire state.

## Try it in 30 seconds

```bash
# Built-in demo key (Fuji testnet, no real value)
export AGW_PRIVATE_KEY=0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6
export AGW_GATEWAY=https://avarouter.up.railway.app

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

> Demo key address: `0x90F79bf6EB2c4f870365E785982E1f101E93b906`. Fuji testnet only.

## Local server (dev)

```bash
# 1. Build
go build -o demo/agw ./cmd/agw

# 2. Start with Fuji defaults (offline mode, no on-chain settlement)
./demo/start.sh           # listens on :8080
./demo/start-mock.sh      # mock LLM upstream on :9999

# 3. Use the CLI against your local server
cd demo/node
node agw-cli/bin/agw.mjs flow 1000
```

## Key technical points

| Layer | Algorithm | What it signs |
|---|---|---|
| Topup authorization | **EIP-3009** (USDC `TransferWithAuthorization`) | "I authorize the gateway to debit N USDC from my wallet" |
| Management calls | **EIP-191** + length-prefix | The canonical request: method, path, body-sha256, timestamp, payer |
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

## Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/v1/topup` | EIP-191 + EIP-3009 | Top up. First call auto-registers. Sessions rejected. |
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

## Server env vars

| Var | Required | Default | Purpose |
|---|---|---|---|
| `AGW_USDC_RPC` | yes | (none) | JSON-RPC URL of the chain |
| `AGW_USDC_ADDRESS` | yes | (none) | USDC contract on that chain |
| `AGW_USDC_CHAIN_ID` | yes | (none) | Numeric chain id (`43113` Fuji, `1` mainnet) |
| `AGW_USDC_SERVER_KEY` | no | (offline) | Hot wallet that submits `transferWithAuthorization`. **If unset → offline mode.** |
| `AGW_USDC_SERVER_KEY_PATH` | no | — | Same, but only file path. |
| `AGW_PAY_TO` | no | unset | If set, all topups are forced to this `payTo` (single-tenant mode). Omit for multi-tenant. |

## Configuring the Mavis SKILL

The skill (`skills/agw-onboard/`) reads:

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

To point at a different gateway:

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
import { AGWClient } from './skills/agw-onboard/agw-cli/src/client.mjs';
const c = new AGWClient({ gateway: 'http://localhost:8080' });
```

## The 14 subcommands

```
onboarding:    topup, login, logout, mint, rotate, revoke
read:          balance, keys, sessions, users, whoami
hot path:      chat
compound:      flow
utility:       config, state, help
```

Run `agw help` for the full reference, or `agw help <command>` for one command.

## License

MIT-style, see upstream.
