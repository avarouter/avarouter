// AGW (x402) client library — a thin wrapper over the AGW HTTP API
// designed to be called by both humans and AI agents.
//
// USAGE
// -----
//   import { AGWClient } from './lib.mjs';
//   const client = new AGWClient({ gateway: process.env.AGW_GATEWAY });
//
//   // Topup (auto-registers the user on first call). Returns the
//   // new balance in micro-USDC.
//   const { balance } = await client.topup({
//     wallet: ethers.Wallet.createRandom(),
//     payTo:  '0x...',     // optional: defaults to wallet.address
//     amount: '1000',      // micro-USDC
//   });
//
//   // Login (POST /v1/auth) — returns a session token.
//   const { sessionToken, expiresAt } = await client.login(wallet);
//
//   // Use the session for management calls.
//   const { key, meta } = await client.mintKey({ sessionToken, wallet });
//   const { items }    = await client.listKeys({ sessionToken });
//
// All methods return `{ status, json, text }` on error so callers
// can surface a helpful error to the user.

import { ethers } from 'ethers';
import { createHash } from 'node:crypto';
import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';

export const MANAGE_VERSION = 'AGW-MANAGE-v1';
export const AUTH_VERSION   = 'AGW-AUTH-v1';
export const SESSION_PREFIX = 'ags_';

// ----- gateway resolution -----
//
// Priority (highest first):
//   1. env var AGW_GATEWAY
//   2. `.env` file in CWD (KEY=VALUE lines)
//   3. ~/.agw/config (KEY=VALUE lines, optional user-global config)
//   4. built-in defaults
//
// Built-in defaults: production Railway deploy first, then localhost.
// Set AGW_GATEWAY explicitly to override.

const DEFAULT_GATEWAYS = [
  'https://avarouter.up.railway.app', // production (Railway)
  'http://127.0.0.1:8080',           // local dev fallback
];

function parseDotenv(contents) {
  const out = {};
  for (const raw of contents.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const m = line.match(/^([A-Z_][A-Z0-9_]*)\s*=\s*(.*?)\s*$/i);
    if (!m) continue;
    let v = m[2];
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    out[m[1]] = v;
  }
  return out;
}

function loadEnvFile(path) {
  if (!existsSync(path)) return {};
  try { return parseDotenv(readFileSync(path, 'utf8')); } catch { return {}; }
}

let _envCache = null;
function loadAllEnv() {
  if (_envCache) return _envCache;
  // Merge in order, later files override earlier.
  const merged = {};
  for (const p of [
    resolve(process.cwd(), '.env'),
    resolve(process.env.HOME || '/root', '.agw', 'config'),
  ]) {
    Object.assign(merged, loadEnvFile(p));
  }
  // Real env vars win over .env files (env vars set by the user
  // in the shell should take precedence over what's on disk).
  for (const k of Object.keys(process.env)) merged[k] = process.env[k];
  _envCache = merged;
  return merged;
}

/** Resolve the gateway URL using the priority above. */
export function resolveGateway(explicit) {
  if (explicit) return explicit.replace(/\/+$/, '');
  const env = loadAllEnv();
  if (env.AGW_GATEWAY) return env.AGW_GATEWAY.replace(/\/+$/, '');
  for (const g of DEFAULT_GATEWAYS) return g.replace(/\/+$/, '');
  return 'http://127.0.0.1:8080';
}

/** Resolve the network name (fuji / mainnet / custom) for diagnostics. */
export function resolveNetwork() {
  const env = loadAllEnv();
  if (env.AGW_NETWORK) return env.AGW_NETWORK;
  if (env.AGW_CHAIN_ID === '1') return 'mainnet';
  return 'fuji-testnet';
}

export class AGWClient {
  constructor({
    gateway,
    chainId,
    usdcAddress,
  } = {}) {
    const env = loadAllEnv();
    this.gateway = resolveGateway(gateway);
    // Network config: env var > constructor arg > Fuji defaults
    this.chainId = chainId
      ?? (env.AGW_CHAIN_ID ? Number(env.AGW_CHAIN_ID) : 43113);
    this.usdcAddress = usdcAddress
      ?? env.AGW_USDC_ADDRESS
      ?? '0x5425890298aed601595a70AB815c96711a31Bc65';
  }

  /** Pretty-print the active config. Useful in scripts. */
  describe() {
    return {
      gateway:     this.gateway,
      network:     resolveNetwork(),
      chainId:     this.chainId,
      usdcAddress: this.usdcAddress,
    };
  }

  // ---------- low-level HTTP ----------

  async _req(method, path, body, hdrs = {}) {
    const r = await fetch(`${this.gateway}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json', ...hdrs },
      body: body !== undefined && body !== null
        ? (typeof body === 'string' ? body : JSON.stringify(body))
        : undefined,
    });
    const text = await r.text();
    let json = null;
    try { json = JSON.parse(text); } catch {}
    return { status: r.status, json, text };
  }

  // ---------- signing helpers ----------

  _buildManageMessage(method, path, body, timestamp, payer) {
    let bodyStr = '';
    if (body !== undefined && body !== null) {
      bodyStr = typeof body === 'string' ? body : JSON.stringify(body);
    }
    const bodyBytes = Buffer.from(bodyStr, 'utf8');
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

  _buildAuthMessage(from, expiresAt, timestamp, payer) {
    return [
      AUTH_VERSION,
      `from=${String(from).toLowerCase()}`,
      `expiresAt=${expiresAt}`,
      `timestamp=${timestamp}`,
      `payer=${String(payer).toLowerCase()}`,
    ].join('\n');
  }

  async _signedHeaders(wallet, method, path, body) {
    const ts = Math.floor(Date.now() / 1000);
    const msg = this._buildManageMessage(method, path, body ?? '', ts, wallet.address);
    const sig = await wallet.signMessage(msg);
    return {
      'X-Payer':         wallet.address.toLowerCase(),
      'X-AGW-Timestamp': String(ts),
      'X-AGW-Signature': sig,
    };
  }

  async _authHeaders(wallet, from, expiresAt) {
    const ts = Math.floor(Date.now() / 1000);
    const msg = this._buildAuthMessage(from, expiresAt, ts, wallet.address);
    const sig = await wallet.signMessage(msg);
    return {
      'X-Payer':         wallet.address.toLowerCase(),
      'X-AGW-Timestamp': String(ts),
      'X-AGW-Signature': sig,
    };
  }

  // ---------- EIP-3009 / EIP-712 (x402) ----------

  async _signX402({ from, to, value, validAfter, validBefore, nonce }, wallet) {
    const domain = {
      name: 'USD Coin', version: '2', chainId: this.chainId,
      verifyingContract: this.usdcAddress,
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
    return wallet.signTypedData(domain, types, {
      from, to, value, validAfter, validBefore, nonce,
    });
  }

  // ---------- topup (auto-registers) ----------

  /**
   * Topup the user's balance. On first call the user is auto-registered.
   *
   * @param {Object} opts
   * @param {ethers.Wallet} opts.wallet
   * @param {string} [opts.payTo]    deposit address (defaults to wallet.address)
   * @param {string|number} opts.amount  micro-USDC
   * @returns {Promise<{balance: bigint, user: string, payTo: string, txHash: string, note: string, raw: object}>}
   */
  async topup({ wallet, payTo, amount }) {
    payTo = (payTo || wallet.address).toLowerCase();
    const user = wallet.address.toLowerCase();

    // 1. signed challenge
    const hdr1 = await this._signedHeaders(wallet, 'POST', '/v1/topup',
      { amount: String(amount), payTo });
    const r1 = await this._req('POST', '/v1/topup',
      { amount: String(amount), payTo }, hdr1);
    if (r1.status !== 402) {
      throw new Error(`topup challenge failed: ${r1.status} ${r1.text}`);
    }
    const accepts = r1.json?.accepts?.[0];
    if (!accepts) throw new Error('topup 402 had no accepts');
    const { payTo: serverPayTo, maxAmountRequired, extra = {} } = accepts;
    const va = BigInt(extra.validAfter  ?? 0);
    const vb = BigInt(extra.validBefore ?? 0);
    const nonce = extra.nonce
      ?? '0x' + [...crypto.getRandomValues(new Uint8Array(32))]
          .map((b) => b.toString(16).padStart(2, '0')).join('');

    // 2. sign EIP-3009, retry with payment
    const sig = await this._signX402({
      from: user, to: serverPayTo,
      value: maxAmountRequired, validAfter: va, validBefore: vb, nonce,
    }, wallet);
    const parsed = ethers.Signature.from(sig);

    const hdr2 = await this._signedHeaders(wallet, 'POST', '/v1/topup', {
      amount: maxAmountRequired,
      payTo: serverPayTo,
      payment: {
        from: user, to: serverPayTo, value: maxAmountRequired,
        validAfter: String(va), validBefore: String(vb), nonce,
        v: parsed.v, r: parsed.r, s: parsed.s,
      },
    });
    const r2 = await this._req('POST', '/v1/topup', {
      amount: maxAmountRequired,
      payTo: serverPayTo,
      payment: {
        from: user, to: serverPayTo, value: maxAmountRequired,
        validAfter: String(va), validBefore: String(vb), nonce,
        v: parsed.v, r: parsed.r, s: parsed.s,
      },
    }, hdr2);
    if (r2.status !== 200) {
      throw new Error(`topup credit failed: ${r2.status} ${r2.text}`);
    }
    return {
      balance: BigInt(r2.json.balance),
      user: r2.json.user,
      payTo: r2.json.payTo,
      txHash: r2.json.txHash || '',
      note: r2.json.note || '',
      raw: r2.json,
    };
  }

  // ---------- auth (POST /v1/auth) ----------

  /**
   * Sign in via /v1/auth. Returns a session token + metadata.
   *
   * @param {ethers.Wallet} wallet
   * @param {Object} [opts]
   * @param {number} [opts.ttlSeconds=3600] absolute expiry
   * @returns {Promise<{sessionToken: string, expiresAt: number, idleExpiresAt: number, user: string}>}
   */
  async login(wallet, { ttlSeconds = 3600 } = {}) {
    const expiresAt = Math.floor(Date.now() / 1000) + ttlSeconds;
    const hdr = await this._authHeaders(wallet, wallet.address, expiresAt);
    const r = await this._req('POST', '/v1/auth',
      { from: wallet.address.toLowerCase(), expiresAt }, hdr);
    if (r.status !== 200) {
      throw new Error(`login failed: ${r.status} ${r.text}`);
    }
    return {
      sessionToken: r.json.sessionToken,
      expiresAt: r.json.expiresAt,
      idleExpiresAt: r.json.idleExpiresAt,
      user: r.json.user,
    };
  }

  /**
   * Logout — invalidates the session token. Idempotent.
   * @param {string} sessionToken
   */
  async logout(sessionToken) {
    return this._req('POST', '/v1/auth/logout', null,
      { 'X-AGW-Session': sessionToken });
  }

  /**
   * List the user's active sessions.
   * @param {string} sessionToken
   */
  async listSessions(sessionToken) {
    const r = await this._req('GET', '/v1/auth/sessions', null,
      { 'X-AGW-Session': sessionToken });
    if (r.status !== 200) throw new Error(`list sessions: ${r.status} ${r.text}`);
    return r.json;
  }

  // ---------- keys (session auth) ----------

  /**
   * Mint a new API key for the user.
   * @param {string} sessionToken
   * @param {ethers.Wallet} [wallet]  if provided, used for per-req sig instead
   * @returns {Promise<{key: string, meta: object}>}
   */
  async mintKey(sessionToken, wallet) {
    const hdr = wallet
      ? await this._signedHeaders(wallet, 'POST', '/v1/keys', {})
      : { 'X-AGW-Session': sessionToken };
    const r = await this._req('POST', '/v1/keys', {}, hdr);
    if (r.status !== 200) throw new Error(`mint key: ${r.status} ${r.text}`);
    return { key: r.json.key, meta: r.json.meta, warning: r.json.warning };
  }

  /**
   * List the user's keys.
   * @param {string} [sessionToken]
   * @param {ethers.Wallet} [wallet]
   * @param {string} [user]   admin-style: ?user=0x... filter
   */
  async listKeys({ sessionToken, wallet, user } = {}) {
    const hdr = wallet
      ? await this._signedHeaders(wallet, 'GET', '/v1/keys', null)
      : sessionToken
        ? { 'X-AGW-Session': sessionToken }
        : {};
    const path = user ? `/v1/keys?user=${user}` : '/v1/keys';
    const r = await this._req('GET', path, null, hdr);
    if (r.status !== 200) throw new Error(`list keys: ${r.status} ${r.text}`);
    return r.json;
  }

  /**
   * Revoke a single key by prefix.
   * @param {string} prefix
   * @param {Object} opts { sessionToken, wallet }
   */
  async revokeKey(prefix, { sessionToken, wallet } = {}) {
    const hdr = wallet
      ? await this._signedHeaders(wallet, 'DELETE', `/v1/keys/${prefix}`, null)
      : { 'X-AGW-Session': sessionToken };
    const r = await this._req('DELETE', `/v1/keys/${prefix}`, null, hdr);
    if (r.status === 404) return { ok: false, reason: 'not found' };
    if (r.status !== 200) throw new Error(`revoke key: ${r.status} ${r.text}`);
    return { ok: true, hash: r.json.hash, prefix: r.json.prefix };
  }

  /**
   * Rotate all of the user's keys: revoke all + issue new.
   * @param {Object} opts { sessionToken, wallet }
   */
  async rotateKeys({ sessionToken, wallet } = {}) {
    const hdr = wallet
      ? await this._signedHeaders(wallet, 'POST', '/v1/keys/rotate', null)
      : { 'X-AGW-Session': sessionToken };
    const r = await this._req('POST', '/v1/keys/rotate', null, hdr);
    if (r.status !== 200) throw new Error(`rotate keys: ${r.status} ${r.text}`);
    return { key: r.json.key, meta: r.json.meta, warning: r.json.warning };
  }

  // ---------- balance / users (read) ----------

  /**
   * Look up a user's balance.
   * @param {Object} opts { user, sessionToken, wallet }
   *   - user:         0x... (admin lookup)
   *   - sessionToken: bearer token (key-owner's balance)
   *   - wallet:       signed X-Payer (own balance)
   */
  async balance({ user, sessionToken, wallet } = {}) {
    let path, hdr = {};
    if (user) {
      path = `/payments/balance?user=${user}`;
    } else if (sessionToken) {
      path = '/payments/balance';
      hdr['X-AGW-Session'] = sessionToken;
    } else if (wallet) {
      path = '/payments/balance';
      hdr['X-Payer'] = wallet.address.toLowerCase();
    } else {
      throw new Error('balance(): need user, sessionToken, or wallet');
    }
    const r = await this._req('GET', path, null, hdr);
    if (r.status !== 200) throw new Error(`balance: ${r.status} ${r.text}`);
    return {
      from:       r.json.from,
      payTo:      r.json.payTo,
      balance:    BigInt(r.json.balance ?? 0),
      balanceUSDC: r.json.balanceUSDC ?? 0,
      registered: r.json.registered ?? true,
      raw:        r.json,
    };
  }

  /**
   * List all registered users (admin/aggregate view).
   * @param {Object} [opts]
   * @param {string} [opts.window]  1h|24h|7d|30d|all
   */
  async listUsers({ window } = {}) {
    const path = window ? `/payments/users?window=${window}` : '/payments/users';
    const r = await this._req('GET', path, null, {});
    if (r.status !== 200) throw new Error(`list users: ${r.status} ${r.text}`);
    return r.json;
  }

  // ---------- hot path (bearer) ----------

  /**
   * Make one hot-path call using an API key. Returns the upstream
   * response. The gateway debits the API key owner's balance.
   * @param {string} apiKey
   * @param {Object} body
   */
  async chat(apiKey, body) {
    const r = await this._req('POST', '/v1/chat/completions', body,
      { Authorization: `Bearer ${apiKey}` });
    if (r.status === 402) return { status: 402, body: r.json, text: r.text };
    if (r.status !== 200) throw new Error(`chat: ${r.status} ${r.text}`);
    return { status: 200, body: r.json, text: r.text };
  }
}

// ---------- wallet utilities ----------

/**
 * Load a wallet from various sources. Priority:
 *   1. explicit `pk` argument
 *   2. env var AGW_PRIVATE_KEY / AGW_PK / <name>_PK
 *   3. AGW_KEYSTORE / AGW_KEYSTORE_PASSWORD (encrypted JSON file)
 *   4. AGW_MNEMONIC (BIP-39)
 *   5. deterministic seed from `name` (DEV ONLY)
 */
export function loadWallet({ name = 'agent', pk, env } = {}) {
  const e = env || loadAllEnv();
  const pkVal = pk
    || e.AGW_PRIVATE_KEY
    || e.AGW_PK
    || e[`${name.toUpperCase()}_PK`];
  if (pkVal) return new ethers.Wallet(pkVal);

  if (e.AGW_KEYSTORE && e.AGW_KEYSTORE_PASSWORD) {
    return ethers.Wallet.fromJson(readFileSync(e.AGW_KEYSTORE, 'utf8'),
      e.AGW_KEYSTORE_PASSWORD);
  }

  if (e.AGW_MNEMONIC) {
    return ethers.HDNodeWallet.fromPhrase(e.AGW_MNEMONIC);
  }

  // Deterministic dev fallback (so example scripts run without
  // a configured wallet). DO NOT use in production.
  return new ethers.Wallet(ethers.id('agw-' + name));
}

/** Print a JSON-friendly view of a wallet. */
export function describeWallet(wallet) {
  return {
    address: wallet.address,
    addressLower: wallet.address.toLowerCase(),
  };
}
