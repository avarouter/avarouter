#!/bin/bash
# Launch the local avarouter server.
# Run from the repo root:  ./demo/start.sh
# Or: AGW_GATEWAY=http://127.0.0.1:8080 ./demo/start.sh
set -e
export PATH=/usr/local/go/bin:/usr/bin:/bin
export AGW_USDC_RPC=${AGW_USDC_RPC:-https://api.avax-test.network/ext/bc/C/rpc}
export AGW_USDC_ADDRESS=${AGW_USDC_ADDRESS:-0x5425890298aed601595a70AB815c96711a31Bc65}
export AGW_USDC_CHAIN_ID=${AGW_USDC_CHAIN_ID:-43113}

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEMO="$ROOT/demo"

# Build if missing
if [ ! -x "$DEMO/agw" ] || [ "$ROOT/cmd/agw/main.go" -nt "$DEMO/agw" ]; then
  echo "[start.sh] building server..."
  (cd "$ROOT" && go build -o "$DEMO/agw" ./cmd/agw)
fi

exec "$DEMO/agw" \
  --config "$DEMO/config.yaml" --listen :8080 \
  --data-dir "$DEMO/data" -log-stderr
