// agw config
//
// Show the active configuration (gateway, network, USDC, etc).

import { describeConfig } from '../config.mjs';

export const cmd = {
  name: 'config',
  description: 'Show the active configuration',
  usage: 'agw config',
};

export async function run({ args, flags, cfg }) {
  const c = describeConfig();
  console.log('config');
  console.log(`  gateway:     ${c.gateway}`);
  console.log(`  network:     ${c.network}`);
  console.log(`  chainId:     ${c.chainId}`);
  console.log(`  usdcAddress: ${c.usdcAddress}`);
  console.log();
  console.log('  Resolution priority:');
  console.log('    1. $AGW_GATEWAY');
  console.log('    2. .env (in CWD)');
  console.log('    3. ~/.agw/config');
  console.log('    4. built-in default (Railway then localhost)');
  console.log();
  console.log('  Override with:');
  console.log('    AGW_GATEWAY=https://... agw config');
}
