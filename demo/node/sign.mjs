// Sign helper for AGW management endpoints.
//
// Two message formats:
//   - AGW-MANAGE-v1: per-request signature for management calls
//   - AGW-AUTH-v1:   one-time signature to mint a session token
//
// Both use EIP-191 personal_sign. ethers' wallet.signMessage()
// adds the standard "\x19Ethereum Signed Message:\n<len>" prefix.

import { createHash } from 'node:crypto';

export const MANAGE_VERSION = 'AGW-MANAGE-v1';
export const AUTH_VERSION   = 'AGW-AUTH-v1';

export function buildManageMessage(method, path, body, timestamp, payer) {
  const bodyBytes = typeof body === 'string' ? Buffer.from(body) : Buffer.from(body ?? '');
  const bodyHash = createHash('sha256').update(bodyBytes).digest('hex');
  return [
    MANAGE_VERSION,
    `method=${method.toUpperCase()}`,
    `path=${path}`,
    `body-sha256=0x${bodyHash}`,
    `timestamp=${timestamp}`,
    `payer=${payer.toLowerCase()}`,
  ].join('\n');
}

export function buildAuthMessage(from, expiresAt, timestamp, payer) {
  return [
    AUTH_VERSION,
    `from=${String(from).toLowerCase()}`,
    `expiresAt=${expiresAt}`,
    `timestamp=${timestamp}`,
    `payer=${String(payer).toLowerCase()}`,
  ].join('\n');
}

export async function signedHeaders(wallet, method, path, body) {
  const ts = Math.floor(Date.now() / 1000);
  // Empty body hashes to the empty-string SHA-256 — server expects
  // this exact format for GET / no-body requests.
  const msg = buildManageMessage(method, path, body ?? '', ts, wallet.address);
  const sig = await wallet.signMessage(msg);
  return {
    'X-Payer':         wallet.address.toLowerCase(),
    'X-AGW-Timestamp': String(ts),
    'X-AGW-Signature': sig,
  };
}

export async function authHeaders(wallet, from, expiresAt) {
  const ts = Math.floor(Date.now() / 1000);
  const msg = buildAuthMessage(from, expiresAt, ts, wallet.address);
  const sig = await wallet.signMessage(msg);
  return {
    'X-Payer':         wallet.address.toLowerCase(),
    'X-AGW-Timestamp': String(ts),
    'X-AGW-Signature': sig,
  };
}

