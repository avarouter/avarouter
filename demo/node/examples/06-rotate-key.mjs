#!/usr/bin/env -S node --no-warnings
// 06-rotate-key.mjs — thin wrapper around `agw rotate`.
import { runAgw } from './_cli.mjs';
await runAgw(['rotate', ...process.argv.slice(2)]);
