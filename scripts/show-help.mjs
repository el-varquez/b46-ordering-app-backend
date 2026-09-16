#!/usr/bin/env node
const commands = [
  ['setup', 'Restore Go dependencies.'],
  ['db-up', 'Start the local PostgreSQL container.'],
  ['db-down', 'Stop local services without deleting data.'],
  ['db-reset', 'Recreate the disposable local database and migrate.'],
  ['migrate', 'Apply all Ordering database migrations.'],
  ['migrate-status', 'Show Ordering migration status.'],
  ['run', 'Start the Ordering API.'],
  ['fmt', 'Format all Go source.'],
  ['vet', 'Run Go static analysis.'],
  ['test', 'Run Go tests once.'],
  ['test-race', 'Run Go tests with the race detector.'],
  ['test-integration', 'Run checks against TEST_DATABASE_URL.'],
  ['architecture', 'Check Clean Architecture dependency rules.'],
  ['contracts', 'Validate OpenAPI and versioned contracts.'],
  ['migrations-check', 'Validate migration structure.'],
  ['check', 'Run every Phase 1 completion gate.'],
];

console.log('B46 backend commands:');
for (const [name, description] of commands) {
  console.log(`  ${name.padEnd(18)} ${description}`);
}
