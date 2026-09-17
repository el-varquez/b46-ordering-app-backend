# Developer workflow

## Configuration

Copy `.env.example` for local values. The Makefile loads `.env` automatically
and exports its values to Go commands; direct `go run` commands still require
the variables to be loaded by your shell or IDE. `DATABASE_URL` is required.
Every other variable has a validated default. No secret value is logged or
returned in an HTTP error.

## Database and migrations

`docker compose up -d --wait postgres` creates the development database. Apply
migrations with:

```text
go -C services/ordering run ./cmd/migrate -dir migrations up
go -C services/ordering run ./cmd/migrate -dir migrations status
```

Migration files are sequential, forward-applied, and append-only after they are
shared. The `Down` section exists only for disposable local/test reset. Never
rewrite an applied migration; add the next numbered file. `make db-reset`
deletes only the named Compose volume and must not be used for retained data.

Integration checks require `TEST_DATABASE_URL`. It must identify a disposable
database because tests create short-lived schemas and exercise constraints. CI
provides PostgreSQL 18.6 automatically.

## Phase 1 gate

`make check` runs contracts, migration structure, architecture, formatting,
static analysis, race-enabled tests, and builds. With `TEST_DATABASE_URL` set,
it also applies migrations twice and runs database integration tests.

## Troubleshooting

- Port 5432 occupied: stop the other PostgreSQL service or change the Compose
  host port and both database URLs.
- Port 8080 occupied: set `HTTP_PORT` to an unused port.
- Readiness returns 503: verify PostgreSQL is reachable and run the migration
  command. Liveness can remain 200 while a running database becomes unavailable.
- Startup exits before listening: correct the named invalid environment value,
  or migrate the database. This fail-fast behavior is intentional.
- Container runtime unavailable: use any local PostgreSQL supported by pgx and
  point `DATABASE_URL` and `TEST_DATABASE_URL` at it.

## Identity development

Access tokens are opaque and short-lived. Refresh tokens rotate on every use;
presenting a consumed refresh token revokes its complete device family. Mobile
clients must therefore serialize refresh through one shared authentication
module. Logout is repeatable and affects only the presented device family,
while disabling an account revokes every family.

Protected requests use only `Authorization: Bearer <access-token>`. Send a
refresh token only in the `/v1/auth/refresh` JSON body. Never put passwords,
access tokens, refresh tokens, provider tokens, complete Authorization headers,
or password hashes in logs, screenshots, issue reports, or committed fixtures.

Google and Apple client IDs are comma-separated audience allowlists. Local
automated tests use generated signing keys and static JWKS data; they must not
depend on live provider endpoints. Provider secrets and developer credentials
stay outside this repository.

Create the initial Admin with `make bootstrap-admin` and runtime-only
`B46_BOOTSTRAP_ADMIN_NAME`, `B46_BOOTSTRAP_ADMIN_EMAIL`, and
`B46_BOOTSTRAP_ADMIN_PASSWORD` values. Repeating the same email is idempotent;
a conflicting Admin email is rejected and bootstrap activity is audited.

For incident-safe troubleshooting, identify requests by correlation ID and
session family metadata. Revoke the affected family or disable the account;
do not request raw credentials from a user.

When Flutter development begins, Android emulators reach a host backend through
`10.0.2.2`; iOS simulators use `127.0.0.1`. The frontend pins a backend tag or
commit for contracts and does not import this repository's Go source.
