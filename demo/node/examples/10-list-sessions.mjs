#!/usr/bin/env -S node --no-warnings
// 10-list-sessions.mjs — thin wrapper around `agw sessions`.
import { runAgw } from './_cli.mjs';
await runAgw(['sessions', ...process.argv.slice(2)]);
