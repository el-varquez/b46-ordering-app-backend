#!/usr/bin/env node
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const directory = join(root, 'services', 'ordering', 'migrations');
const files = readdirSync(directory).filter((file) => file.endsWith('.sql')).sort();
const failures = [];

if (files.length === 0) failures.push('no Ordering migrations found');
files.forEach((file, index) => {
  const expectedPrefix = String(index + 1).padStart(5, '0');
  if (!file.startsWith(`${expectedPrefix}_`)) failures.push(`${file}: expected sequential prefix ${expectedPrefix}_`);
  const content = readFileSync(join(directory, file), 'utf8');
  if (!content.includes('-- +goose Up')) failures.push(`${file}: missing -- +goose Up`);
  if (!content.includes('-- +goose Down')) failures.push(`${file}: missing documented disposable-database rollback`);
  if (/\b(MONEY|REAL|DOUBLE PRECISION)\b/i.test(content)) failures.push(`${file}: money must use integer centavos`);
  if (/\bTIMESTAMP\b(?!\s+WITH\s+TIME\s+ZONE)/i.test(content.replaceAll('timestamptz', ''))) {
    failures.push(`${file}: timestamps must be timezone-aware`);
  }
});

if (failures.length > 0) {
  console.error('check:migrations FAILED\n');
  failures.forEach((failure) => console.error(failure));
  process.exit(1);
}
console.log(`check:migrations OK — ${files.length} append-only Ordering migration(s) validated`);
