#!/usr/bin/env -S node --no-warnings
// 09-list-users.mjs — thin wrapper around `agw users`.
import { runAgw } from './_cli.mjs';
await runAgw(['users', ...process.argv.slice(2)]);
