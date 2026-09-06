// Config + env resolution for the agw CLI
//
// Priority for AGW_GATEWAY (highest first):
//   1. env var AGW_GATEWAY
//   2. `.env` file in CWD
//   3. ~/.agw/config
//   4. built-in default (Railway production deploy)
//
// Priority for AGW_PRIVATE_KEY:
//   1. explicit `pk` argument
//   2. env var AGW_PRIVATE_KEY / AGW_PK / <name>_PK
//   3. AGW_KEYSTORE / AGW_KEYSTORE_PASSWORD
//   4. AGW_MNEMONIC
//   5. deterministic dev fallback (name → ethers.id)

import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';

export const MANAGE_VERSION = 'AGW-MANAGE-v1';
export const AUTH_VERSION   = 'AGW-AUTH-v1';
export const SESSION_PREFIX = 'ags_';
export const API_KEY_PREFIX = 'agw_';

const DEFAULT_GATEWAYS = [
  'https://avarouter.up.railway.app', // production (Railway)
  'http://127.0.0.1:8080',           // local dev fallback
];

const FUJI_USDC = '0x5425890298aed601595a70AB815c96711a31Bc65';
const MAINNET_USDC = '0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48';

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
export function loadAllEnv() {
  if (_envCache) return _envCache;
  const merged = {};
  for (const p of [
    resolve(process.cwd(), '.env'),
    resolve(process.env.HOME || '/root', '.agw', 'config'),
  ]) {
    Object.assign(merged, loadEnvFile(p));
  }
  // Real env vars win over .env files.
  for (const k of Object.keys(process.env)) merged[k] = process.env[k];
  _envCache = merged;
  return merged;
}

/** Reset the env cache (test-only). */
export function _resetEnvCache() { _envCache = null; }

export function resolveGateway(explicit) {
  if (explicit) return explicit.replace(/\/+$/, '');
  const env = loadAllEnv();
  if (env.AGW_GATEWAY) return env.AGW_GATEWAY.replace(/\/+$/, '');
  for (const g of DEFAULT_GATEWAYS) return g.replace(/\/+$/, '');
  return 'http://127.0.0.1:8080';
}

export function resolveNetwork() {
  const env = loadAllEnv();
  if (env.AGW_NETWORK) return env.AGW_NETWORK;
  if (env.AGW_CHAIN_ID === '1') return 'mainnet';
  return 'fuji-testnet';
}

export function resolveChainId() {
  const env = loadAllEnv();
  return env.AGW_CHAIN_ID ? Number(env.AGW_CHAIN_ID) : 43113;
}

export function resolveUsdcAddress() {
  const env = loadAllEnv();
  if (env.AGW_USDC_ADDRESS) return env.AGW_USDC_ADDRESS;
  if (resolveChainId() === 1) return MAINNET_USDC;
  return FUJI_USDC;
}

/** Print the active config. */
export function describeConfig(gateway) {
  return {
    gateway:     resolveGateway(gateway),
    network:     resolveNetwork(),
    chainId:     resolveChainId(),
    usdcAddress: resolveUsdcAddress(),
  };
}
