// Wallet helpers — load a wallet from env, keystore, mnemonic, or dev seed.

import { readFileSync } from 'node:fs';
import { ethers } from 'ethers';
import { loadAllEnv } from './config.mjs';

export function loadWallet({ name = 'agent', pk } = {}) {
  const env = loadAllEnv();
  const pkVal = pk
    || env.AGW_PRIVATE_KEY
    || env.AGW_PK
    || env[`${name.toUpperCase()}_PK`];
  if (pkVal) return new ethers.Wallet(pkVal);

  if (env.AGW_KEYSTORE && env.AGW_KEYSTORE_PASSWORD) {
    return ethers.Wallet.fromJson(
      readFileSync(env.AGW_KEYSTORE, 'utf8'),
      env.AGW_KEYSTORE_PASSWORD
    );
  }

  if (env.AGW_MNEMONIC) {
    return ethers.HDNodeWallet.fromPhrase(env.AGW_MNEMONIC);
  }

  // Deterministic dev fallback so the CLI runs without setup.
  return new ethers.Wallet(ethers.id('agw-' + name));
}

export function describeWallet(wallet) {
  return {
    address:      wallet.address,
    addressLower: wallet.address.toLowerCase(),
  };
}
