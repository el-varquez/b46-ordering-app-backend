# B46 Ordering Backend

Initial Clean Architecture scaffold for the B46 ordering system.

- `services/ordering`: public Ordering backend.
- `services/inventory-adapter`: private adapter for the store inventory database.
- `contracts`: versioned public and private wire contracts.
- `database`: Ordering database support files.
- `tests`: cross-module contract and integration checks.

The directories intentionally contain no implementation yet. Development starts
with Phase 1 after this scaffold is committed.

## Continuous Integration

Three independent GitHub Actions workflows protect the repository:

- **Build & Test CI** discovers every Go module and runs dependency download,
  vet, race-enabled tests, and build. The empty architecture scaffold is valid,
  but Go source without an owning `go.mod` fails.
- **Architecture CI** enforces the dependency lock, Clean Architecture directory
  grammar, inward dependency direction, and separation between Ordering and the
  inventory adapter.
- **Conventions CI** requires `<type>/<kebab-name>` branches and Conventional
  Commit subjects on pull requests.

Local checks:

```text
node scripts/check-build-test.mjs
node scripts/check-architecture.mjs
node scripts/check-conventions.mjs --branch ci/example --subject "ci: add checks"
```
