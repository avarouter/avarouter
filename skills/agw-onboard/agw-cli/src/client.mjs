// AGWClient — thin HTTP client for the AGW gateway.
//
// Designed for both programmatic use (import AGWClient) and
// the CLI (each command instantiates one).
//
// Auth modes for management endpoints:
//   - sessionToken  →  X-AGW-Session
//   - wallet        →  X-Payer + X-AGW-Timestamp + X-AGW-Signature
//
// Hot path (chat): Authorization: Bearer <apiKey>

import { ethers } from 'ethers';
import { resolveGateway, resolveChainId, resolveUsdcAddress } from './config.mjs';
import { signedManageHeaders, signedAuthHeaders, signX402 } from './crypto.mjs';

export class AGWClient {
  constructor({ gateway, chainId, usdcAddress } = {}) {
    this.gateway     = resolveGateway(gateway);
    this.chainId     = chainId     ?? resolveChainId();
    this.usdcAddress = usdcAddress ?? resolveUsdcAddress();
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

  // ---------- topup (auto-registers) ----------

  async topup({ wallet, payTo, amount }) {
    payTo = (payTo || wallet.address).toLowerCase();
    const user = wallet.address.toLowerCase();

    // 1. signed challenge
    const hdr1 = await signedManageHeaders(wallet, 'POST', '/v1/topup',
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
    const sig = await signX402(wallet, this.chainId, this.usdcAddress, {
      from: user, to: serverPayTo, value: maxAmountRequired,
      validAfter: va, validBefore: vb, nonce,
    });
    const parsed = ethers.Signature.from(sig);

    const body = {
      amount: maxAmountRequired,
      payTo: serverPayTo,
      payment: {
        from: user, to: serverPayTo, value: maxAmountRequired,
        validAfter: String(va), validBefore: String(vb), nonce,
        v: parsed.v, r: parsed.r, s: parsed.s,
      },
    };
    const hdr2 = await signedManageHeaders(wallet, 'POST', '/v1/topup', body);
    const r2 = await this._req('POST', '/v1/topup', body, hdr2);
    if (r2.status !== 200) {
      throw new Error(`topup credit failed: ${r2.status} ${r2.text}`);
    }
    return {
      balance: BigInt(r2.json.balance),
      user:    r2.json.user,
      payTo:   r2.json.payTo,
      txHash:  r2.json.txHash || '',
      note:    r2.json.note || '',
      raw:     r2.json,
    };
  }

  // ---------- auth ----------

  async login(wallet, { ttlSeconds = 3600 } = {}) {
    const expiresAt = Math.floor(Date.now() / 1000) + ttlSeconds;
    const hdr = await signedAuthHeaders(wallet, wallet.address, expiresAt);
    const r = await this._req('POST', '/v1/auth',
      { from: wallet.address.toLowerCase(), expiresAt }, hdr);
    if (r.status !== 200) {
      throw new Error(`login failed: ${r.status} ${r.text}`);
    }
    return {
      sessionToken:   r.json.sessionToken,
      expiresAt:      r.json.expiresAt,
      idleExpiresAt:  r.json.idleExpiresAt,
      user:           r.json.user,
    };
  }

  async logout(sessionToken) {
    return this._req('POST', '/v1/auth/logout', null,
      { 'X-AGW-Session': sessionToken });
  }

  async listSessions(sessionToken) {
    const r = await this._req('GET', '/v1/auth/sessions', null,
      { 'X-AGW-Session': sessionToken });
    if (r.status !== 200) throw new Error(`list sessions: ${r.status} ${r.text}`);
    return r.json;
  }

  // ---------- keys (session OR sig) ----------

  async mintKey({ sessionToken, wallet } = {}) {
    const hdr = wallet
      ? await signedManageHeaders(wallet, 'POST', '/v1/keys', {})
      : { 'X-AGW-Session': sessionToken };
    const r = await this._req('POST', '/v1/keys', {}, hdr);
    if (r.status !== 200) throw new Error(`mint key: ${r.status} ${r.text}`);
    return { key: r.json.key, meta: r.json.meta, warning: r.json.warning };
  }

  async listKeys({ sessionToken, wallet, user } = {}) {
    const hdr = wallet
      ? await signedManageHeaders(wallet, 'GET', '/v1/keys', null)
      : sessionToken ? { 'X-AGW-Session': sessionToken } : {};
    const path = user ? `/v1/keys?user=${user}` : '/v1/keys';
    const r = await this._req('GET', path, null, hdr);
    if (r.status !== 200) throw new Error(`list keys: ${r.status} ${r.text}`);
    return r.json;
  }

  async revokeKey(prefix, { sessionToken, wallet } = {}) {
    const hdr = wallet
      ? await signedManageHeaders(wallet, 'DELETE', `/v1/keys/${prefix}`, null)
      : { 'X-AGW-Session': sessionToken };
    const r = await this._req('DELETE', `/v1/keys/${prefix}`, null, hdr);
    if (r.status === 404) return { ok: false, reason: 'not found' };
    if (r.status !== 200) throw new Error(`revoke key: ${r.status} ${r.text}`);
    return { ok: true, hash: r.json.hash, prefix: r.json.prefix };
  }

  async rotateKeys({ sessionToken, wallet } = {}) {
    const hdr = wallet
      ? await signedManageHeaders(wallet, 'POST', '/v1/keys/rotate', null)
      : { 'X-AGW-Session': sessionToken };
    const r = await this._req('POST', '/v1/keys/rotate', null, hdr);
    if (r.status !== 200) throw new Error(`rotate keys: ${r.status} ${r.text}`);
    return { key: r.json.key, meta: r.json.meta, warning: r.json.warning };
  }

  // ---------- balance / users (read) ----------

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
    // Aggregate response (no auth) has `total`; per-user has `from`.
    if (r.json.from) {
      return {
        from:        r.json.from,
        payTo:       r.json.payTo,
        balance:     BigInt(r.json.balance ?? 0),
        balanceUSDC: r.json.balanceUSDC ?? 0,
        registered:  r.json.registered ?? true,
        raw:         r.json,
      };
    }
    return {
      from:        null,
      payTo:       null,
      balance:     BigInt(r.json.total ?? 0),
      balanceUSDC: r.json.totalUSDC ?? 0,
      userCount:   r.json.userCount ?? 0,
      network:     r.json.network,
      asset:       r.json.asset,
      raw:         r.json,
    };
  }

  async listUsers({ window } = {}) {
    const path = window ? `/payments/users?window=${window}` : '/payments/users';
    const r = await this._req('GET', path, null, {});
    if (r.status !== 200) throw new Error(`list users: ${r.status} ${r.text}`);
    return r.json;
  }

  // ---------- hot path (bearer) ----------

  async chat(apiKey, body) {
    const r = await this._req('POST', '/v1/chat/completions', body,
      { Authorization: `Bearer ${apiKey}` });
    if (r.status === 402) return { status: 402, body: r.json, text: r.text };
    if (r.status !== 200) throw new Error(`chat: ${r.status} ${r.text}`);
    return { status: 200, body: r.json, text: r.text };
  }
}
