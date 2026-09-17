#!/usr/bin/env node
import { spawnSync } from 'node:child_process';

const commands = [
  ['node', ['scripts/check-contracts.mjs']],
  ['node', ['scripts/check-migrations.mjs']],
  ['node', ['scripts/check-architecture.mjs']],
  ['node', ['scripts/check-build-test.mjs']],
];

for (const [command, args] of commands) {
  const result = spawnSync(command, args, { stdio: 'inherit' });
  if (result.error || result.status !== 0) process.exit(result.status ?? 1);
}

console.log('\ncheck:phase2 OK — identity, sessions, OAuth, contracts, and architecture passed');
