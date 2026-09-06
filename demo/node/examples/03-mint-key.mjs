#!/usr/bin/env -S node --no-warnings
// 03-mint-key.mjs — thin wrapper around `agw mint`.
import { runAgw } from './_cli.mjs';
await runAgw(['mint', ...process.argv.slice(2)]);
