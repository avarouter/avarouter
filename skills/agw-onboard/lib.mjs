// Backwards-compat re-export. New code should import directly:
//
//   import { AGWClient } from './agw-cli/src/client.mjs';
//   import { loadWallet } from './agw-cli/src/wallet.mjs';
//   import { resolveGateway, describeConfig } from './agw-cli/src/config.mjs';

export {
  AGWClient,
  loadWallet,
  describeWallet,
} from './agw-cli/src/wallet.mjs';
export { resolveGateway, resolveNetwork, describeConfig } from './agw-cli/src/config.mjs';

// For true back-compat with the old examples that imported from lib.mjs:
export { AGWClient as default } from './agw-cli/src/client.mjs';
