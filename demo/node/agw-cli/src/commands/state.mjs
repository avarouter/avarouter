// agw state [show|clear|path]
//
// Inspect or clear the on-disk state file.

import { loadState, statePath, clearState } from '../state.mjs';

export const cmd = {
  name: 'state',
  description: 'Inspect or clear the saved state file',
  usage: 'agw state [show|clear|path]',
  examples: [
    'agw state',
    'agw state show',
    'agw state clear',
    'agw state path',
  ],
};

export async function run({ args, flags, cfg }) {
  const sub = args[0] || 'show';
  if (sub === 'path') {
    console.log(statePath());
    return;
  }
  if (sub === 'clear') {
    clearState();
    console.log('state cleared');
    return;
  }
  // default: show
  const st = loadState();
  console.log('state');
  console.log(`  path:     ${statePath()}`);
  if (st) {
    console.log(`  gateway:  ${st.gateway || '(unset)'}`);
    console.log(`  user:     ${st.user || '(unset)'}`);
    console.log(`  updated:  ${st.updatedAt || '(never)'}`);
  } else {
    console.log('  (no state file)');
  }
}
