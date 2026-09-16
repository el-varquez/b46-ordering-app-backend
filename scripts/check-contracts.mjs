#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const readJSON = (path) => JSON.parse(readFileSync(join(root, path), 'utf8'));
const failures = [];

function typeMatches(value, expected) {
  if (expected === 'object') return value !== null && typeof value === 'object' && !Array.isArray(value);
  if (expected === 'array') return Array.isArray(value);
  if (expected === 'integer') return Number.isInteger(value);
  return typeof value === expected;
}

function validate(value, schema, path = '$') {
  if (schema.type && !typeMatches(value, schema.type)) {
    failures.push(`${path}: expected ${schema.type}`);
    return;
  }
  if ('const' in schema && value !== schema.const) failures.push(`${path}: expected constant ${JSON.stringify(schema.const)}`);
  if (schema.enum && !schema.enum.includes(value)) failures.push(`${path}: value ${JSON.stringify(value)} is not in the enum`);
  if (typeof value === 'string') {
    if (schema.minLength !== undefined && value.length < schema.minLength) failures.push(`${path}: string is too short`);
    if (schema.maxLength !== undefined && value.length > schema.maxLength) failures.push(`${path}: string is too long`);
    if (schema.format === 'uuid' && !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)) {
      failures.push(`${path}: expected UUID`);
    }
    if (schema.format === 'date-time' && Number.isNaN(Date.parse(value))) failures.push(`${path}: expected RFC 3339 date-time`);
  }
  if (typeof value === 'number' && schema.minimum !== undefined && value < schema.minimum) failures.push(`${path}: value is below ${schema.minimum}`);
  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) failures.push(`${path}: array has fewer than ${schema.minItems} items`);
    if (schema.items) value.forEach((item, index) => validate(item, schema.items, `${path}[${index}]`));
  }
  if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
    for (const required of schema.required ?? []) {
      if (!(required in value)) failures.push(`${path}: missing required property ${required}`);
    }
    if (schema.additionalProperties === false) {
      for (const key of Object.keys(value)) {
        if (!(key in (schema.properties ?? {}))) failures.push(`${path}: unknown property ${key}`);
      }
    }
    for (const [key, child] of Object.entries(schema.properties ?? {})) {
      if (key in value) validate(value[key], child, `${path}.${key}`);
    }
  }
}

const pairs = [
  ['contracts/inventory/v1/inventory-commit-requested.schema.json', 'contracts/inventory/v1/inventory-commit-requested.example.json'],
  ['contracts/inventory/v1/inventory-committed.schema.json', 'contracts/inventory/v1/inventory-committed.example.json'],
  ['contracts/inventory/v1/inventory-items-unavailable.schema.json', 'contracts/inventory/v1/inventory-items-unavailable.example.json'],
];
for (const [schemaPath, examplePath] of pairs) {
  try {
    validate(readJSON(examplePath), readJSON(schemaPath), examplePath);
  } catch (error) {
    failures.push(`${examplePath}: ${error.message}`);
  }
}

try {
  const openapi = readJSON('contracts/http/openapi.json');
  if (openapi.openapi !== '3.1.1') failures.push('contracts/http/openapi.json: expected OpenAPI 3.1.1');
  for (const route of ['/v1/health/live', '/v1/health/ready']) {
    if (!openapi.paths?.[route]?.get) failures.push(`contracts/http/openapi.json: missing GET ${route}`);
  }
  const expectedVocabulary = {
    Role: ['CUSTOMER', 'CASHIER', 'ADMIN'],
    OrderStatus: ['SUBMITTED', 'CONFIRMED', 'REJECTED'],
    FulfillmentStatus: ['PREPARING', 'DELIVERING', 'DELIVERED'],
  };
  for (const [schema, values] of Object.entries(expectedVocabulary)) {
    const actual = openapi.components?.schemas?.[schema]?.enum;
    if (JSON.stringify(actual) !== JSON.stringify(values)) failures.push(`contracts/http/openapi.json: ${schema} vocabulary drifted`);
  }
  const serialized = JSON.stringify(openapi.components?.schemas ?? {});
  for (const hiddenState of ['PENDING', 'PUBLISHED', 'InventoryCommitRequested']) {
    if (serialized.includes(hiddenState)) failures.push(`contracts/http/openapi.json: customer contract exposes backend state ${hiddenState}`);
  }
} catch (error) {
  failures.push(`contracts/http/openapi.json: ${error.message}`);
}

if (failures.length > 0) {
  console.error('check:contracts FAILED\n');
  failures.forEach((failure) => console.error(failure));
  process.exit(1);
}
console.log(`check:contracts OK — OpenAPI and ${pairs.length} inventory examples are valid`);
