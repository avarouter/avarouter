#!/usr/bin/env -S node --no-warnings
// 02-login.mjs — thin wrapper around `agw login`.
import { runAgw } from './_cli.mjs';
await runAgw(['login', ...process.argv.slice(2)]);
