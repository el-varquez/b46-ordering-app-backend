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
  const identityOperations = [
    ['post', '/v1/auth/password/login'],
    ['post', '/v1/auth/oauth/intents'],
    ['post', '/v1/auth/oauth/login'],
    ['post', '/v1/auth/refresh'],
    ['post', '/v1/auth/logout'],
    ['get', '/v1/me'],
    ['post', '/v1/me/oauth-link-intents'],
    ['post', '/v1/me/oauth-identities'],
  ];
  for (const [method, route] of identityOperations) {
    if (!openapi.paths?.[route]?.[method]) {
      failures.push(`contracts/http/openapi.json: missing ${method.toUpperCase()} ${route}`);
    }
  }
  const orderingOperations = [
    ['post', '/v1/orders'],
    ['get', '/v1/orders'],
    ['get', '/v1/orders/{order_id}'],
    ['get', '/v1/staff/orders'],
    ['get', '/v1/staff/orders/{order_id}'],
    ['post', '/v1/staff/orders/{order_id}/read'],
    ['patch', '/v1/staff/orders/{order_id}/status'],
  ];
  for (const [method, route] of orderingOperations) {
    const operation = openapi.paths?.[route]?.[method];
    if (!operation) {
      failures.push(`contracts/http/openapi.json: missing ${method.toUpperCase()} ${route}`);
      continue;
    }
    if (JSON.stringify(operation.security) !== JSON.stringify([{ bearerAuth: [] }])) {
      failures.push(`contracts/http/openapi.json: ${method.toUpperCase()} ${route} must require bearerAuth`);
    }
  }
  const bearer = openapi.components?.securitySchemes?.bearerAuth;
  if (bearer?.type !== 'http' || bearer?.scheme !== 'bearer' || bearer?.bearerFormat !== 'opaque') {
    failures.push('contracts/http/openapi.json: bearerAuth must describe opaque HTTP bearer tokens');
  }
  for (const [method, route] of [
    ['post', '/v1/auth/logout'],
    ['get', '/v1/me'],
    ['post', '/v1/me/oauth-link-intents'],
    ['post', '/v1/me/oauth-identities'],
  ]) {
    const security = openapi.paths?.[route]?.[method]?.security;
    if (JSON.stringify(security) !== JSON.stringify([{ bearerAuth: [] }])) {
      failures.push(`contracts/http/openapi.json: ${method.toUpperCase()} ${route} must require bearerAuth`);
    }
  }
  const expectedVocabulary = {
    Role: ['CUSTOMER', 'CASHIER', 'ADMIN'],
    Provider: ['PASSWORD', 'GOOGLE', 'APPLE'],
    OAuthProvider: ['GOOGLE', 'APPLE'],
    AccountStatus: ['ACTIVE', 'DISABLED'],
    OrderStatus: ['SUBMITTED', 'CONFIRMED', 'REJECTED'],
    FulfillmentStatus: ['PREPARING', 'DELIVERING', 'DELIVERED'],
    CustomerOrderStatus: ['PREPARING', 'ON_THE_WAY', 'DELIVERED', 'REJECTED'],
  };
  for (const [schema, values] of Object.entries(expectedVocabulary)) {
    const actual = openapi.components?.schemas?.[schema]?.enum;
    if (JSON.stringify(actual) !== JSON.stringify(values)) failures.push(`contracts/http/openapi.json: ${schema} vocabulary drifted`);
  }
  const serialized = JSON.stringify(openapi.components?.schemas ?? {});
  const errorCodes = openapi.components?.schemas?.Error?.properties?.code?.enum ?? [];
  for (const code of [
    'INVALID_CREDENTIALS',
    'INVALID_OAUTH_CREDENTIAL',
    'INVALID_OAUTH_INTENT',
    'IDENTITY_LINK_REQUIRED',
    'IDENTITY_ALREADY_LINKED',
    'ACCOUNT_DISABLED',
    'CART_CHANGED',
    'INVALID_TRANSITION',
  ]) {
    if (!errorCodes.includes(code)) failures.push(`contracts/http/openapi.json: missing error code ${code}`);
  }
  if (JSON.stringify(openapi).includes('password_hash')) {
    failures.push('contracts/http/openapi.json: password_hash must never appear in the public contract');
  }
  for (const hiddenState of ['PENDING', 'PUBLISHED', 'InventoryCommitRequested']) {
    if (serialized.includes(hiddenState)) failures.push(`contracts/http/openapi.json: customer contract exposes backend state ${hiddenState}`);
  }
  for (const hiddenField of [
    'available_quantity',
    'operation_id',
    'event_id',
    'checkout_fingerprint',
    'claim_token',
    'attempt_count',
    'last_error_code',
  ]) {
    if (serialized.includes(hiddenField)) failures.push(`contracts/http/openapi.json: public schema exposes internal field ${hiddenField}`);
  }
  const checkoutProperties = Object.keys(openapi.components?.schemas?.PlaceOrderRequest?.properties ?? {});
  for (const clientOwned of ['customer_id', 'product_name', 'subtotal_centavos', 'total_centavos', 'status', 'role']) {
    if (checkoutProperties.includes(clientOwned)) failures.push(`contracts/http/openapi.json: PlaceOrderRequest lets clients set ${clientOwned}`);
  }
} catch (error) {
  failures.push(`contracts/http/openapi.json: ${error.message}`);
}

try {
  const privateOpenAPI = readJSON('contracts/inventory/http/openapi.json');
  if (privateOpenAPI.openapi !== '3.1.1') failures.push('contracts/inventory/http/openapi.json: expected OpenAPI 3.1.1');
  const commit = privateOpenAPI.paths?.['/inventory/commit']?.post;
  if (!commit) failures.push('contracts/inventory/http/openapi.json: missing POST /inventory/commit');
  if (JSON.stringify(commit?.security) !== JSON.stringify([{ serviceBearer: [] }])) {
    failures.push('contracts/inventory/http/openapi.json: inventory commit must require serviceBearer');
  }
  for (const route of ['/health/live', '/health/ready']) {
    if (!privateOpenAPI.paths?.[route]?.get) failures.push(`contracts/inventory/http/openapi.json: missing GET ${route}`);
  }
  const bearer = privateOpenAPI.components?.securitySchemes?.serviceBearer;
  if (bearer?.type !== 'http' || bearer?.scheme !== 'bearer') {
    failures.push('contracts/inventory/http/openapi.json: serviceBearer must be HTTP bearer authentication');
  }
} catch (error) {
  failures.push(`contracts/inventory/http/openapi.json: ${error.message}`);
}

if (failures.length > 0) {
  console.error('check:contracts FAILED\n');
  failures.forEach((failure) => console.error(failure));
  process.exit(1);
}
console.log(`check:contracts OK — OpenAPI and ${pairs.length} inventory examples are valid`);
