# B46 Ordering Backend

Go modular monolith for B46 Ordering, plus the contract boundary for the future
inventory adapter. Phase 3 proves idempotent checkout, the transactional
outbox, fake inventory outcomes, Customer order history, the Cashier queue,
and the Preparing to Delivering to Delivered lifecycle. Password and
Google/Apple identity use opaque mobile sessions with rotating refresh tokens.

## Pinned toolchain

- Go `1.27.1` (also recorded in `.go-version` and the Go workspace)
- PostgreSQL `18.6` for the local and CI containers
- Goose `v3` as the migration runner, pinned by `go.mod` and `go.sum`

Go 1.27.1 and any container runtime supporting Compose are required. Node.js
22 or newer runs the dependency-free repository checks.

## Start locally

```text
copy .env.example .env
make db-reset
make run
```

Make loads `.env` automatically. Direct Go commands still require the variable
in the current shell: PowerShell uses `$env:DATABASE_URL = "..."`; Bash uses
`export DATABASE_URL="..."`.
The API exposes health, identity, Customer-order, and Cashier-order routes on
port 8080. Protected routes use
`Authorization: Bearer <access-token>`; refresh tokens are accepted only by the
refresh operation's JSON body. No browser cookie authentication is used.

Run `make help` for all commands. `make check` is the single Phase 3 gate. Set
`TEST_DATABASE_URL` to a disposable migrated PostgreSQL database to include the
database integration suite; CI always does this.

## Initial Admin

The backend has no public Admin-registration route. Supply the first Admin only
at runtime, then remove the sensitive values from the shell:

```powershell
$env:B46_BOOTSTRAP_ADMIN_NAME = "B46 Admin"
$env:B46_BOOTSTRAP_ADMIN_EMAIL = "admin@gmail.com"
$env:B46_BOOTSTRAP_ADMIN_PASSWORD = Read-Host "Temporary Admin password"
make bootstrap-admin
Remove-Item Env:B46_BOOTSTRAP_ADMIN_NAME
Remove-Item Env:B46_BOOTSTRAP_ADMIN_EMAIL
Remove-Item Env:B46_BOOTSTRAP_ADMIN_PASSWORD
```

The same email is idempotent. A different email is rejected after the first
Admin exists. The password is Argon2id-hashed and is never printed.

## OAuth development configuration

Set `GOOGLE_CLIENT_IDS` and `APPLE_CLIENT_IDS` to comma-separated Android/iOS
audiences outside version control. Development may leave them empty; OAuth
then fails closed. Production startup rejects missing provider audiences. The
regular test suite signs local fixtures and never contacts live providers.

## Repository map

- `services/ordering`: Ordering composition root, identity, order lifecycle, and worker.
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
and the [developer workflow](docs/development.md) before changing lifecycle code.
