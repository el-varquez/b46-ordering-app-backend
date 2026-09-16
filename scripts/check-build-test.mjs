#!/usr/bin/env node
/**
 * Build and test every Go module in the repository.
 *
 * The initial repository intentionally has no go.mod files. The check accepts
 * that scaffold, but fails if Go source appears before its owning module is
 * initialized. As modules are added, the same command automatically checks
 * formatting, downloads, vets, tests with the race detector, and builds each
 * one. When TEST_DATABASE_URL is present it also migrates a disposable database
 * and runs integration-tagged checks.
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
  const goSources = files.filter((file) => file.startsWith(`${moduleDir}${sep}`) && file.endsWith('.go'));
  if (goSources.length > 0) {
    const result = spawnSync('gofmt', ['-l', ...goSources], {
      cwd: moduleDir,
      encoding: 'utf8',
    });
    if (result.error || result.status !== 0) {
      console.error(`check:build-test FAILED — gofmt could not inspect ${rel(moduleDir)}`);
      process.exit(result.status ?? 1);
    }
    if (result.stdout.trim()) {
      console.error('check:build-test FAILED — Go files require formatting');
      console.error(result.stdout.trim());
      process.exit(1);
    }
  }
  run(moduleDir, 'go', ['mod', 'download']);
  run(moduleDir, 'go', ['vet', './...']);
  run(moduleDir, 'go', ['test', '-count=1', './...']);
  const cgo = spawnSync('go', ['env', 'CGO_ENABLED'], {
    cwd: moduleDir,
    encoding: 'utf8',
  });
  if (cgo.status === 0 && cgo.stdout.trim() === '1') {
    run(moduleDir, 'go', ['test', '-race', '-count=1', './...']);
  } else {
    console.warn(`[${rel(moduleDir)}] race tests skipped because this Go installation has CGO disabled; CI runs them`);
  }
  run(moduleDir, 'go', ['build', './...']);

  if (process.env.TEST_DATABASE_URL && rel(moduleDir) === 'services/ordering') {
    const environment = { ...process.env, DATABASE_URL: process.env.TEST_DATABASE_URL };
    const runWithEnvironment = (args) => {
      const display = `go ${args.join(' ')}`;
      console.log(`\n[${rel(moduleDir)}] ${display}`);
      const result = spawnSync('go', args, {
        cwd: moduleDir,
        encoding: 'utf8',
        stdio: 'inherit',
        env: environment,
      });
      if (result.error || result.status !== 0) {
        console.error(`check:build-test FAILED — ${display} failed`);
        process.exit(result.status ?? 1);
      }
    };
    runWithEnvironment(['run', './cmd/migrate', '-dir', 'migrations', 'up']);
    runWithEnvironment(['run', './cmd/migrate', '-dir', 'migrations', 'up']);
    runWithEnvironment(['test', '-tags=integration', '-count=1', './...']);
    if (cgo.status === 0 && cgo.stdout.trim() === '1') {
      runWithEnvironment(['test', '-race', '-tags=integration', '-count=1', './...']);
    }
  }
}

console.log(`\ncheck:build-test OK — ${moduleFiles.length} Go module(s) built and tested`);
