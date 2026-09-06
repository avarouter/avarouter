#!/usr/bin/env -S node --no-warnings
// 11-chat.mjs — thin wrapper around `agw chat`.
import { runAgw } from './_cli.mjs';
await runAgw(['chat', ...process.argv.slice(2)]);
