#!/usr/bin/env -S node --no-warnings
// 01-topup.mjs — thin wrapper around `agw topup`.
// New code should call:
//   $ agw topup 1000
import { runAgw } from './_cli.mjs';
await runAgw(['topup', ...process.argv.slice(2)]);
