# B46 Ordering Backend

Go modular monolith for B46 Ordering, plus the contract boundary for the future
inventory adapter. Phase 1 supplies the production-shaped process foundation,
PostgreSQL schema, versioned contracts, and enforceable architecture rules. It
does not implement identity, ordering use cases, or inventory synchronization.

## Pinned toolchain

- Go `1.27.1` (also recorded in `.go-version` and the Go workspace)
- PostgreSQL `18.6` for the local and CI containers
- Goose `v3` as the migration runner, pinned by `go.mod` and `go.sum`

Go 1.27.1 and any container runtime supporting Compose are required. Node.js
22 or newer runs the dependency-free repository checks.

## Start locally

```text
copy .env.example .env
docker compose up -d --wait postgres
set DATABASE_URL=postgres://b46:b46_local_only@localhost:5432/b46_ordering?sslmode=disable
go -C services/ordering run ./cmd/migrate -dir migrations up
go -C services/ordering run ./cmd/api
```

PowerShell uses `$env:DATABASE_URL = "..."`; Bash uses `export DATABASE_URL="..."`.
The API exposes `GET /v1/health/live` and `GET /v1/health/ready` on port 8080.

Run `make help` for all commands. `make check` is the single Phase 1 gate. Set
`TEST_DATABASE_URL` to a disposable migrated PostgreSQL database to include the
database integration suite; CI always does this.

## Repository map

- `services/ordering`: Ordering composition root and Phase 1 platform behavior.
- `services/inventory-adapter`: reserved structure; implementation starts in Phase 4.
- `contracts/http`: source-of-truth OpenAPI contract.
- `contracts/inventory/v1`: versioned Ordering/adapter JSON schemas and fixtures.
- `architecture`: dependency lock and architecture-checker self-test.
- `scripts`: the same gates used locally and in CI.
- `docs`: architecture, contracts, migration, and troubleshooting guidance.

## Continuous Integration

Three independent GitHub Actions workflows protect the repository:

- **Build & Test CI** starts disposable PostgreSQL, applies migrations twice to
  prove idempotent startup, then runs formatting, vet, race-enabled unit and
  integration tests, and builds every Go package.
- **Architecture CI** enforces the dependency lock, Clean Architecture directory
  grammar, inward dependency direction, and separation between Ordering and the
  inventory adapter.
- **Conventions CI** requires `<type>/<kebab-name>` branches and Conventional
  Commit subjects on pull requests.

Local checks:

```text
node scripts/check-build-test.mjs
node scripts/check-architecture.mjs
node scripts/check-contracts.mjs
node scripts/check-migrations.mjs
node scripts/check-conventions.mjs --branch ci/example --subject "ci: add checks"
```

See [backend architecture](docs/architecture/backend.md), [contracts](contracts/README.md),
and the [developer workflow](docs/development.md) before beginning Phase 2.
