#!/usr/bin/env -S node --no-warnings
// 99-full-flow.mjs — thin wrapper around `agw flow` (one-shot onboarding).
import { runAgw } from './_cli.mjs';
await runAgw(['flow', ...process.argv.slice(2)]);
