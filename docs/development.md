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

## Phase 3 gate

`make check` runs contracts, migration structure, architecture, formatting,
static analysis, race-enabled tests, and builds. With `TEST_DATABASE_URL` set,
it also applies migrations twice and runs database integration tests.

## Order lifecycle development

The local API uses a deterministic checkout catalog containing `COKE-1.5L`,
`TASTY-BREAD`, and `FRESH-MILK-1L`. Prices are integer centavos. Placing an
order writes its line snapshots, pending inventory operation, and outbox event
in one transaction. The in-process worker commits through the Phase 3 fake
inventory adapter and creates the unread `PREPARING` fulfillment. There is no
inventory-checking screen or cancellation state in the public contract.

Customer routes require an exact `CUSTOMER` access token. Cashier routes under
`/v1/staff/orders` require an exact `CASHIER` token; Admin is not implicitly a
Cashier. A newly placed order is immediately represented to the Customer as
`PREPARING`. Cashier transitions are only `PREPARING -> DELIVERING ->
DELIVERED`, while Customers receive `ON_THE_WAY` for the middle state.

Use a new UUID for each logical `checkout_id` and reuse that UUID only when
retrying the identical Place Order request. The same ID with changed input is
a conflict. Order lists use `limit` and `after_id` keyset pagination. Opening a
Cashier detail is read-only; call the separate `/read` action to clear its New
badge.

The fake catalog and inventory adapters exist only to prove the lifecycle.
Phase 4 replaces the inventory fake through the existing `InventoryCommitter`
interface; it must not move inventory details into the public HTTP response.

For the complete PostgreSQL suite, set `TEST_DATABASE_URL` to the disposable
test database, migrate it to version 3, and run:

```text
go -C services/ordering test -tags=integration -count=1 ./...
```

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
