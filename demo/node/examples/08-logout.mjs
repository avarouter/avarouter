#!/usr/bin/env -S node --no-warnings
// 08-logout.mjs — thin wrapper around `agw logout`.
import { runAgw } from './_cli.mjs';
await runAgw(['logout', ...process.argv.slice(2)]);
