#!/usr/bin/env -S node --no-warnings
// 04-list-keys.mjs — thin wrapper around `agw keys`.
import { runAgw } from './_cli.mjs';
await runAgw(['keys', ...process.argv.slice(2)]);
