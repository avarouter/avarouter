// State persistence — stores sessionToken, apiKey, and metadata
// at ~/.agw/state.json (or $AGW_STATE_DIR/state.json) so subsequent
// CLI invocations don't need to re-auth.
//
// File format:
//   {
//     "gateway":          "https://avarouter.up.railway.app",
//     "user":             "0x...",
//     "payTo":            "0x...",
//     "sessionToken":     "ags_...",
//     "sessionExpiresAt": <unix sec>,
//     "apiKey":           "agw_...",
//     "apiKeyPrefix":     "agw_dPak",
//     "apiKeyCreatedAt":  <unix sec>,
//     "lastBalance":      <micro-USDC>,
//     "updatedAt":        <iso>
//   }

import { existsSync, mkdirSync, readFileSync, writeFileSync, unlinkSync } from 'node:fs';
import { resolve, join } from 'node:path';

export function stateDir() {
  return process.env.AGW_STATE_DIR
    || resolve(process.env.HOME || '/root', '.agw');
}

export function statePath() {
  return join(stateDir(), 'state.json');
}

export function loadState() {
  const p = statePath();
  if (!existsSync(p)) return null;
  try { return JSON.parse(readFileSync(p, 'utf8')); }
  catch { return null; }
}

export function saveState(patch) {
  const dir = stateDir();
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
  const cur = loadState() || {};
  const next = { ...cur, ...patch, updatedAt: new Date().toISOString() };
  writeFileSync(statePath(), JSON.stringify(next, null, 2) + '\n');
  return next;
}

export function clearState() {
  const p = statePath();
  if (existsSync(p)) unlinkSync(p);
}

/** Mask secrets for display. */
export function maskToken(t, keep = 8) {
  if (!t) return '';
  if (t.length <= keep * 2) return t.slice(0, keep) + '…';
  return t.slice(0, keep) + '…' + t.slice(-keep);
}
