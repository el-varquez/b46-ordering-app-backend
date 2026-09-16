#!/usr/bin/env node
/**
 * Build and test every Go module in the repository.
 *
 * The initial repository intentionally has no go.mod files. The check accepts
 * that scaffold, but fails if Go source appears before its owning module is
 * initialized. As modules are added, the same command automatically downloads,
 * vets, tests with the race detector, and builds each one.
 */
import { readdirSync } from 'node:fs';
import { dirname, join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = fileURLToPath(new URL('..', import.meta.url));
const skipped = new Set(['.git', 'bin', 'build', 'coverage', 'dist', 'tmp', 'vendor']);

function* walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (skipped.has(entry.name)) continue;
      yield* walk(join(dir, entry.name));
      continue;
    }
    yield join(dir, entry.name);
  }
}

const rel = (path) => relative(root, path).split(sep).join('/');
const files = [...walk(root)];
const moduleFiles = files.filter((file) => file.endsWith(`${sep}go.mod`)).sort();
const goFiles = files.filter((file) => file.endsWith('.go'));

if (moduleFiles.length === 0) {
  if (goFiles.length > 0) {
    console.error('check:build-test FAILED — Go source exists without an owning go.mod');
    for (const file of goFiles) console.error(rel(file));
    process.exit(1);
  }
  console.log('check:build-test OK — architecture scaffold contains no Go modules yet');
  process.exit(0);
}

const run = (moduleDir, command, args) => {
  const display = `${command} ${args.join(' ')}`;
  console.log(`\n[${rel(moduleDir)}] ${display}`);
  const result = spawnSync(command, args, {
    cwd: moduleDir,
    encoding: 'utf8',
    stdio: 'inherit',
    shell: process.platform === 'win32',
  });
  if (result.error) {
    console.error(`check:build-test FAILED — could not run ${display}: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) {
    console.error(`check:build-test FAILED — ${display} exited with ${result.status}`);
    process.exit(result.status ?? 1);
  }
};

for (const moduleFile of moduleFiles) {
  const moduleDir = dirname(moduleFile);
  run(moduleDir, 'go', ['mod', 'download']);
  run(moduleDir, 'go', ['vet', './...']);
  run(moduleDir, 'go', ['test', '-race', '-count=1', './...']);
  run(moduleDir, 'go', ['build', './...']);
}

console.log(`\ncheck:build-test OK — ${moduleFiles.length} Go module(s) built and tested`);
