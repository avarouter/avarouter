#!/usr/bin/env -S node --no-warnings
// 05-check-balance.mjs — thin wrapper around `agw balance`.
import { runAgw } from './_cli.mjs';
await runAgw(['balance', ...process.argv.slice(2)]);
