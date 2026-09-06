# AGW x402 demo (single-user mode)

End-to-end demo of the AGW prepaid balance gateway in single-user
mode. The flow is exactly what `btwiuse/agw` was extended with:

```
  /v1/topup (x402 V2)            charge route
  ┌──────────────────────────────────────┐
  │ POST → 402 + accepts (payTo, nonce)  │  ←─ signs EIP-3009 transferWithAuthorization
  │ POST → verify sig → credit balance  │  ←─ balance credited on success
  └──────────────────────────────────────┘
  /v1/keys (X-Payer = owner)
  ┌──────────────────────────────────────┐
  │ POST   → generate new API key       │  ←─ plaintext returned ONCE
  │ GET    → list keys (audit trail)    │  ←─ only metadata, no plaintext
  │ POST /v1/keys/rotate                │  ←─ revoke all, issue new
  └──────────────────────────────────────┘
  Hot path (Authorization: Bearer <key>)
  ┌──────────────────────────────────────┐
  │ /v1/chat/completions → upstream     │  ←─ check balance > 0
  │ on response: parse usage, debit     │  ←─ 402 if balance exhausted
  └──────────────────────────────────────┘
```

All on-chain interactions happen ONLY in `/v1/topup`. Once the
balance is credited, the rest is pure server-side bookkeeping in
`wallet.jsonl`.

## Files

- `node/client.mjs` — Node.js test client (uses ethers v6 for signing)
- `node/package.json` — dependencies
- `mock-upstream.py` — minimal OpenAI-compatible mock that returns
  fake usage data so the gateway's usage-based charging has something
  to bill against

## Run the demo

### 1. Start the mock upstream

```bash
python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        self.rfile.read(n)
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({
            'id': 'chatcmpl-mock', 'object': 'chat.completion', 'model': 'mock-model',
            'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': 'hi'}}],
            'usage': {'prompt_tokens': 12, 'completion_tokens': 8, 'total_tokens': 20}
        }).encode())
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', 9999), H).serve_forever()
" &
```

### 2. Generate a keypair for the owner and start AGW

```bash
node -e "
import('ethers').then(({ethers}) => {
  const w = ethers.Wallet.createRandom();
  console.log('OWNER=' + w.address);
  console.log('OWNER_PK=' + w.privateKey);
});
"
# output:
#   OWNER=0xA4da2Ab7A6DCD8d60030900e95027aaa3aF22d18
#   OWNER_PK=0x4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6

# use the OWNER address as --pay-to
export OWNER=0xA4da2Ab7A6DCD8d60030900e95027aaa3aF22d18
export OWNER_PK=0x4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6

mkdir -p /tmp/agw-data
go run ./cmd/agw \
  --config /tmp/agw-data/config.yaml \
  --listen :8080 \
  --pay-to $OWNER \
  --data-dir /tmp/agw-data \
  -log-stderr
```

The config (`config.yaml`):
```yaml
debug: false
appSelectors: []
upstreams:
  - name: mock-echo
    url: http://localhost:9999/v1
    authorization:
      type: none
```

### 3. Run the Node.js demo

```bash
cd demo/node
npm install --registry https://registry.npmmirror.com

OWNER_PRIVATE_KEY=$OWNER_PK \
  AGW_GATEWAY=http://127.0.0.1:8080 \
  node client.mjs
```

## Expected output (truncated)

```
[07:27:57] owner address: 0xA4da2Ab7A6DCD8d60030900e95027aaa3aF22d18
[07:27:57] server owner:  0xa4da2ab7a6dcd8d60030900e95027aaa3af22d18
[07:27:57] balance: 900 micro-USDC

=== Step 1: x402 topup ===
[07:27:57]   402 received: payTo=0xa4da2ab7a6dcd8d60030900e95027aaa3aF22d18 amount=1000
[07:27:57]   signed authorization (v=27)
[07:27:57]   → HTTP 200: {"ok":true,"balance":"1900","note":"offline mode..."}

=== Step 2: generate API key ===
[07:27:57]   API key (save it!): agw_gEN6-W0dnUecFmHu70jeZ7aEZ5swKtXTLM8CPtE0Q9o

=== Step 3: spend with API key ===
[07:27:57]   call #1: HTTP 200 tokens=20 balance=1800
[07:27:57]   call #2: HTTP 200 tokens=20 balance=1700
  ... (drains to ~100) ...

=== Step 4: rejection at balance = 0 ===
[07:27:58]   drain attempt #1: HTTP 200 balance=0
[07:27:58]   drain attempt #2: HTTP 402 balance=0
[07:27:58]   ✓ correctly rejected with 402: balance exhausted — top up via POST /v1/topup

=== Step 5: rotate API key ===
[07:27:58]   new API key: agw_yPmNwLZABlTKW-8zSieCBN6xEoCBlORpwcq6JMSQmHM

=== Step 6: old key must fail ===
[07:27:58]   with old key: HTTP 401 invalid or revoked API key
[07:27:58]   ✓ old key correctly revoked

=== Step 7: topup again to refill balance ===
[07:27:58]   topup #2: HTTP 200, new balance: 1000

=== Step 8: spend with rotated key ===
[07:27:58]   → HTTP 200: {"id": "chatcmpl-mock", ...}

=== Step 9: list keys (audit) ===
[07:27:58]   {count: 9, items: [{active: true, ...}, {active: false, ...}, ...]}

=== Demo complete ===
```

## What the demo proves

| Scenario | Expected | Verified |
|---|---|---|
| `POST /v1/topup` round-trip 1: 402 + accepts | yes | ✓ |
| EIP-712 signature verified server-side | yes | ✓ |
| `POST /v1/topup` round-trip 2: 200 + balance | yes | ✓ |
| `POST /v1/keys` returns plaintext ONCE | yes | ✓ |
| `Authorization: Bearer <key>` accepted | yes | ✓ |
| Usage parsed from upstream response, balance debited | yes | ✓ |
| Balance=0 → `402 Payment Required` | yes | ✓ |
| `POST /v1/keys/rotate` revokes old, issues new | yes | ✓ |
| Old key returns 401 | yes | ✓ |
| Re-topup after exhaustion | yes | ✓ |
| Key audit trail (all keys ever issued) | yes | ✓ |
| Wrong `X-Payer` → 403 | yes (single-user) | ✓ |
| No `Authorization` header → 401 | yes | ✓ |

## Going online (real USDC on Fuji testnet)

The demo runs in "offline mode" — signatures are verified but the
USDC `transferWithAuthorization` tx is not broadcast. To enable
on-chain settlement:

1. Fund a server key with Fuji AVAX (gas): https://faucet.avax.network/
2. Add `--usdc-server-key /path/to/server-private-key.hex` to the
   `agw` command. The first call to `/v1/topup` will then submit a
   real USDC `transferWithAuthorization` to Fuji and wait for
   confirmation before crediting the balance.

For production-grade use, replace the demo mock with real upstream
credentials and configure the `pricing:` block in `config.yaml` so
each call charges by actual token cost instead of the minimum
(default 100 micro-USDC = $0.0001 per call).
