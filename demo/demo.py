#!/usr/bin/env python3
"""Demo: full topup + spend cycle against the AGW x402 gateway on Fuji testnet.

This is a minimal client using only eth_account (signing) and eth_utils (RLP/hex).
Run: pip install eth_account eth_utils requests
"""
import json
import sys
import time
import requests
from eth_account import Account
from eth_account.messages import encode_typed_data
from eth_utils import keccak, to_checksum_address

GATEWAY = "http://127.0.0.1:8080"
USDC_FUJI = "0x5425890298aed601595a70AB815c96711a31Bc65"
CHAIN_ID = 43113

# 1) Generate a payer keypair (for demo — in real use, load from keystore)
acct = Account.create()
payer = acct.address
print(f"=== Demo: AGW x402 prepaid gateway ===")
print(f"Payer: {payer}")
print(f"Private key: {acct.key.hex()}")
print()

# 2) Round-trip 1: ask for payment requirements
print(">>> POST /v1/topup (no payment yet)")
r = requests.post(f"{GATEWAY}/v1/topup",
    headers={"X-Payer": payer, "Content-Type": "application/json"},
    json={"amount": "1000000"})  # 1 USDC
print(f"HTTP {r.status_code}")
if r.status_code != 402:
    print("UNEXPECTED:", r.text)
    sys.exit(1)
spec = r.json()
accepts = spec["accepts"][0]
# Server returns lowercased addresses; eth_account needs checksummed form.
pay_to = to_checksum_address(accepts["payTo"])
asset = to_checksum_address(accepts["asset"])
payer_cs = to_checksum_address(payer)
print(f"  scheme={accepts['scheme']} network={accepts['network']} asset={asset}")
print(f"  payTo={pay_to} amount={accepts['maxAmountRequired']}")

# 3) Sign the EIP-712 TransferWithAuthorization
now = int(time.time())
nonce = "0x" + keccak(payer.encode() + str(now).encode()).hex()
TYPES = {
    "EIP712Domain": [
        {"name": "name", "type": "string"},
        {"name": "version", "type": "string"},
        {"name": "chainId", "type": "uint256"},
        {"name": "verifyingContract", "type": "address"},
    ],
    "TransferWithAuthorization": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "value", "type": "uint256"},
        {"name": "validAfter", "type": "uint256"},
        {"name": "validBefore", "type": "uint256"},
        {"name": "nonce", "type": "bytes32"},
    ],
}
typed_data = {
    "primaryType": "TransferWithAuthorization",
    "types": TYPES,
    "domain": {
        "name": "USD Coin",
        "version": "2",
        "chainId": CHAIN_ID,
        "verifyingContract": USDC_FUJI,
    },
    "message": {
        "from": payer_cs,
        "to": pay_to,
        "value": int(accepts["maxAmountRequired"]),
        "validAfter": now - 60,
        "validBefore": now + 300,
        "nonce": nonce,
    },
}
signable = encode_typed_data(full_message=typed_data)
signed = Account.sign_message(signable, acct.key)
# eth_account v0.14 returns v ∈ {27, 28} for typed data (EIP-712).
# The server accepts both {0, 1} and {27, 28} and normalizes internally.
v_eth712 = signed.v
if v_eth712 not in (27, 28) and v_eth712 in (0, 1):
    v_eth712 += 27
print(f"  v={v_eth712}")

auth = {
    "from": payer_cs,
    "to": pay_to,
    "value": accepts["maxAmountRequired"],
    "validAfter": str(now - 60),
    "validBefore": str(now + 300),
    "nonce": nonce,
    "v": v_eth712,
    "r": "0x" + signed.r.to_bytes(32, "big").hex(),
    "s": "0x" + signed.s.to_bytes(32, "big").hex(),
}

# 4) Round-trip 2: submit signed authorization
print()
print(">>> POST /v1/topup (with signed payment)")
r2 = requests.post(f"{GATEWAY}/v1/topup",
    headers={"X-Payer": payer, "Content-Type": "application/json"},
    json={"amount": "1000000", "payment": auth})
print(f"HTTP {r2.status_code}")
print(json.dumps(r2.json(), indent=2))
if r2.status_code != 200:
    print("TOPUP FAILED")
    sys.exit(1)

# 5) Check balance
print()
print(">>> GET /payments/balance/{addr}")
r3 = requests.get(f"{GATEWAY}/payments/balance/{payer}")
print(f"HTTP {r3.status_code}")
print(json.dumps(r3.json(), indent=2))

# 6) Make an upstream call (will be settled / refunded)
print()
print(">>> POST /v1/chat/completions (upstream call)")
r4 = requests.post(f"{GATEWAY}/v1/chat/completions",
    headers={"X-Payer": payer, "Content-Type": "application/json"},
    json={"model": "mock", "messages": [{"role":"user","content":"hi"}]})
print(f"HTTP {r4.status_code}")
print(r4.text[:200])

# 7) Final balance
print()
print(">>> Final balance")
r5 = requests.get(f"{GATEWAY}/payments/balance/{payer}")
print(f"HTTP {r5.status_code}")
print(json.dumps(r5.json(), indent=2))
print()
print(f"=== Demo complete ===")
