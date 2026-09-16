#!/usr/bin/env node
/**
 * Architecture guard for the B46 Go Clean Architecture layout.
 *
 * R1: every go.mod is declared in architecture/dependencies.json and its direct
 *     requirements match the lock by module name.
 * R2: Go files stay inside the approved service, feature, and layer grammar.
 * R3: inward dependency direction is preserved across Clean Architecture layers.
 * R4: Ordering and inventory-adapter implementations never import each other.
 */
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const lock = JSON.parse(readFileSync(join(root, 'architecture', 'dependencies.json'), 'utf8'));
const lockedModules = lock.modules ?? {};
const skipped = new Set(['.git', 'bin', 'build', 'coverage', 'dist', 'tmp', 'vendor']);

function* walk(dir) {
  if (!existsSync(dir)) return;
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
const hits = [];

const requiredDirectories = [
  'services/ordering/cmd/api',
  'services/ordering/internal/identity',
  'services/ordering/internal/catalog',
  'services/ordering/internal/ordering',
  'services/ordering/internal/admin',
  'services/ordering/internal/notification',
  'services/ordering/internal/platform',
  'services/inventory-adapter/cmd/adapter',
  'services/inventory-adapter/internal/inventory',
  'services/inventory-adapter/internal/platform',
  'contracts/http',
  'contracts/inventory/v1',
  'tests/contract',
  'tests/integration',
];
for (const directory of requiredDirectories) {
  if (!existsSync(join(root, directory))) {
    hits.push(`${directory}: R2 required architecture directory is missing`);
  }
}

const allFiles = [...walk(root)];
const goModuleFiles = allFiles.filter((file) => file.endsWith(`${sep}go.mod`));

const parseRequires = (text) => {
  const names = new Set();
  for (const match of text.matchAll(/^require\s+([^\s(]+)\s+v\S+/gm)) names.add(match[1]);
  for (const block of text.matchAll(/require\s*\(([\s\S]*?)\)/g)) {
    for (const line of block[1].split(/\r?\n/)) {
      const match = line.trim().match(/^([^\s/][^\s]*)\s+v\S+/);
      if (match) names.add(match[1]);
    }
  }
  return [...names].sort();
};

const foundModulePaths = new Set(goModuleFiles.map(rel));
for (const moduleFile of foundModulePaths) {
  if (!(moduleFile in lockedModules)) {
    hits.push(`${moduleFile}: R1 Go module is missing from architecture/dependencies.json`);
    continue;
  }
  const actual = parseRequires(readFileSync(join(root, moduleFile), 'utf8'));
  const expected = [...(lockedModules[moduleFile] ?? [])].sort();
  for (const name of expected) {
    if (!actual.includes(name)) hits.push(`${moduleFile}: R1 missing required dependency "${name}"`);
  }
  for (const name of actual) {
    if (!expected.includes(name)) {
      hits.push(`${moduleFile}: R1 undeclared dependency "${name}" — update the dependency lock in the same PR`);
    }
  }
}
for (const moduleFile of Object.keys(lockedModules)) {
  if (!foundModulePaths.has(moduleFile)) {
    hits.push(`${moduleFile}: R1 dependency lock entry points to a missing go.mod`);
  }
}

const layers = new Set(['domain', 'application', 'ports', 'adapters', 'transport']);
const orderingFeatures = new Set(['identity', 'catalog', 'ordering', 'admin', 'notification']);
const orderingPlatform = new Set(['config', 'postgres', 'httpserver', 'observability']);
const adapterPlatform = new Set(['config', 'httpserver', 'observability']);

const classify = (file) => {
  const path = rel(file);
  const parts = path.split('/');
  if (parts[0] !== 'services') return null;
  const service = parts[1];
  if (parts[2] === 'cmd') return { path, service, kind: 'composition' };
  if (parts[2] !== 'internal') return { path, service, kind: 'invalid' };
  if (parts[3] === 'platform') {
    return { path, service, kind: 'platform', area: parts[4] };
  }
  return { path, service, kind: 'feature', feature: parts[3], layer: parts[4] };
};

const importsOf = (text) => {
  const imports = new Set();
  for (const match of text.matchAll(/^\s*import\s+(?:[._A-Za-z][\w.]*\s+)?"([^"]+)"/gm)) {
    imports.add(match[1]);
  }
  for (const block of text.matchAll(/import\s*\(([\s\S]*?)\)/g)) {
    for (const match of block[1].matchAll(/"([^"]+)"/g)) imports.add(match[1]);
  }
  return [...imports];
};

const forbiddenLayerImports = {
  domain: new Set(['application', 'ports', 'adapters', 'transport', 'platform']),
  application: new Set(['adapters', 'transport', 'platform']),
  ports: new Set(['application', 'adapters', 'transport', 'platform']),
  adapters: new Set(['transport']),
  transport: new Set(['adapters', 'platform']),
};

for (const file of allFiles.filter((candidate) => candidate.endsWith('.go'))) {
  const info = classify(file);
  if (info?.kind === 'invalid') {
    hits.push(`${info.path}: R2 Go source must live under cmd/ or internal/`);
    continue;
  }
  if (info?.kind === 'platform') {
    const allowed = info.service === 'ordering' ? orderingPlatform : adapterPlatform;
    if (!allowed.has(info.area)) {
      hits.push(`${info.path}: R2 unknown ${info.service} platform area "${info.area}"`);
    }
  }
  if (info?.kind === 'feature') {
    const validFeature = info.service === 'ordering'
      ? orderingFeatures.has(info.feature)
      : info.service === 'inventory-adapter' && info.feature === 'inventory';
    if (!validFeature || !layers.has(info.layer)) {
      hits.push(`${info.path}: R2 breaks <service>/internal/<feature>/{domain|application|ports|adapters|transport}`);
      continue;
    }
  }

  for (const imported of importsOf(readFileSync(file, 'utf8'))) {
    if (info?.service === 'ordering' && imported.includes('/services/inventory-adapter/')) {
      hits.push(`${info.path}: R4 Ordering cannot import inventory-adapter implementation`);
    }
    if (info?.service === 'inventory-adapter' && imported.includes('/services/ordering/')) {
      hits.push(`${info.path}: R4 inventory-adapter cannot import Ordering implementation`);
    }
    if (info?.kind !== 'feature') continue;

    const internal = imported.match(/\/services\/[^/]+\/internal\/(?:([^/]+)\/)?([^/]+)(?:\/|$)/);
    if (!internal) continue;
    const importedArea = internal[1] === 'platform' ? 'platform' : internal[2];
    if (forbiddenLayerImports[info.layer]?.has(importedArea)) {
      hits.push(`${info.path}: R3 ${info.layer} cannot import inward-violating area "${importedArea}" from ${imported}`);
    }

    const featurePath = imported.match(/\/services\/ordering\/internal\/([^/]+)\/([^/]+)/);
    if (
      info.service === 'ordering' &&
      featurePath &&
      featurePath[1] !== info.feature &&
      (featurePath[2] === 'adapters' || featurePath[2] === 'transport')
    ) {
      hits.push(`${info.path}: R3 feature "${info.feature}" cannot import ${featurePath[1]}/${featurePath[2]}`);
    }
  }
}

if (hits.length > 0) {
  console.error('check:architecture FAILED — Clean Architecture rules violated\n');
  for (const hit of hits) console.error(hit);
  process.exit(1);
}

console.log('check:architecture OK — dependency lock, directory grammar, dependency direction, and service isolation hold');
