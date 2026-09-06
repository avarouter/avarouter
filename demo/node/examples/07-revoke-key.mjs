#!/usr/bin/env -S node --no-warnings
// 07-revoke-key.mjs — thin wrapper around `agw revoke`.
import { runAgw } from './_cli.mjs';
await runAgw(['revoke', ...process.argv.slice(2)]);
