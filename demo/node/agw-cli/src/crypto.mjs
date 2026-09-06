// Crypto helpers: EIP-191 management signature + EIP-3009 x402 signature.
//
// Message formats:
//
//   AGW-MANAGE-v1        (EIP-191 signed)
//   method=POST
//   path=/v1/keys
//   body-sha256=0x<hex>
//   timestamp=<unix sec>
//   payer=0x...
//
//   AGW-AUTH-v1          (EIP-191 signed)
//   from=0x...
//   expiresAt=<unix sec>
//   timestamp=<unix sec>
//   payer=0x...

import { createHash } from 'node:crypto';
import { MANAGE_VERSION, AUTH_VERSION } from './config.mjs';

export function buildManageMessage(method, path, body, timestamp, payer) {
  let bodyStr = '';
  if (body !== undefined && body !== null) {
    bodyStr = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const bodyHash = createHash('sha256').update(bodyStr, 'utf8').digest('hex');
  return [
    MANAGE_VERSION,
    `method=${method.toUpperCase()}`,
    `path=${path}`,
    `body-sha256=0x${bodyHash}`,
    `timestamp=${timestamp}`,
    `payer=${String(payer).toLowerCase()}`,
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

/** Sign a request with the wallet and return the management headers. */
export async function signedManageHeaders(wallet, method, path, body) {
  const ts = Math.floor(Date.now() / 1000);
  const msg = buildManageMessage(method, path, body ?? '', ts, wallet.address);
  const sig = await wallet.signMessage(msg);
  return {
    'X-Payer':         wallet.address.toLowerCase(),
    'X-AGW-Timestamp': String(ts),
    'X-AGW-Signature': sig,
  };
}

/** Sign an auth request and return the headers. */
export async function signedAuthHeaders(wallet, from, expiresAt) {
  const ts = Math.floor(Date.now() / 1000);
  const msg = buildAuthMessage(from, expiresAt, ts, wallet.address);
  const sig = await wallet.signMessage(msg);
  return {
    'X-Payer':         wallet.address.toLowerCase(),
    'X-AGW-Timestamp': String(ts),
    'X-AGW-Signature': sig,
  };
}

/** Sign an EIP-3009 x402 TransferWithAuthorization. */
export async function signX402(wallet, chainId, usdcAddress, params) {
  const { from, to, value, validAfter, validBefore, nonce } = params;
  const domain = {
    name: 'USD Coin', version: '2',
    chainId, verifyingContract: usdcAddress,
  };
  const types = {
    TransferWithAuthorization: [
      { name: 'from',        type: 'address' },
      { name: 'to',          type: 'address' },
      { name: 'value',       type: 'uint256' },
      { name: 'validAfter',  type: 'uint256' },
      { name: 'validBefore', type: 'uint256' },
      { name: 'nonce',       type: 'bytes32' },
    ],
  };
  return wallet.signTypedData(domain, types, { from, to, value, validAfter, validBefore, nonce });
}
