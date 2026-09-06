// Shared helper for the example wrappers: shell out to the agw CLI.

import { spawn } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
export const agwBin = resolve(here, '..', 'agw-cli', 'bin', 'agw.mjs');

export function runAgw(args) {
  return new Promise((resolveP, rejectP) => {
    const child = spawn('node', [agwBin, ...args],
      { cwd: resolve(here, '..'), stdio: 'inherit' });
    child.on('exit', (c) => c === 0 ? resolveP() : rejectP(new Error(`exit ${c}`)));
  });
}
