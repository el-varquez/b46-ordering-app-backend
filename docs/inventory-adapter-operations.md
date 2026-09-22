# Inventory adapter operations

The inventory adapter is a private Go process. Ordering owns the order,
`operation_id`, outbox retry schedule, and customer/cashier projections. The
adapter owns only its durable receipts and the atomic stock transaction in the
store database. No adapter state or stock quantity belongs in the mobile API.

## Local disposable environment

| Service | Database | Host port |
| --- | --- | --- |
| Ordering PostgreSQL | `b46_ordering` | `5433` |
| POS-compatible fixture | `b46_store_test` | `5434` |

Start both with `docker compose up -d --wait postgres store-postgres`. Apply
Ordering migrations with `make migrate` and the adapter receipt migration with
`make adapter-migrate`. `make store-db-reset` removes only the explicitly named
`b46-ordering_b46-store-postgres-data` volume. It must never target retained
`pos_db` data.

Run the adapter with runtime-only values:

```text
STORE_DATABASE_URL=postgres://<runtime-user>:<password>@localhost:5434/b46_store_test?sslmode=disable
INVENTORY_SERVICE_TOKEN=<runtime-only-token-of-at-least-32-bytes>
POS_MOVEMENT_ACTOR_ID=b4600000-0000-4000-8000-000000000046
make adapter-run
```

Run Ordering in real mode with the same token and
`INVENTORY_ADAPTER_MODE=HTTP`, `INVENTORY_ADAPTER_URL=http://127.0.0.1:8081`.
Fake mode is for focused development/tests only; production rejects it.

## Configuration

### Adapter process

| Variable | Purpose |
| --- | --- |
| `STORE_DATABASE_URL` | Runtime connection to the store database. |
| `INVENTORY_SERVICE_TOKEN` | Opaque Ordering-to-adapter credential; never log or commit it. |
| `POS_MOVEMENT_ACTOR_ID` | Dedicated non-human actor written to stock movements. |
| `HTTP_HOST`, `HTTP_PORT` | Private listener, default `0.0.0.0:8081`. |
| `MAX_REQUEST_BODY_BYTES` | Strict request limit, default 256 KiB. |
| `DATABASE_HEALTH_TIMEOUT` | Store readiness deadline. |

### Ordering process

| Variable | Purpose |
| --- | --- |
| `INVENTORY_ADAPTER_MODE` | `HTTP` for real inventory; `FAKE` only outside production. |
| `INVENTORY_ADAPTER_URL` | Absolute private URL; HTTPS is required in production. |
| `INVENTORY_ADAPTER_TOKEN` | The adapter service credential. |
| `INVENTORY_ADAPTER_TIMEOUT` | One HTTP attempt deadline. The worker owns retries. |
| `INVENTORY_MAX_RESPONSE_BODY_BYTES` | Strict response limit. |

## Database ownership and grants

Use a migration role to create/migrate `b46_adapter` and a different runtime
role for the process. Substitute the deployed role name while connected as a
database owner:

```sql
GRANT CONNECT ON DATABASE pos_db TO b46_adapter_runtime;
GRANT USAGE ON SCHEMA b46_adapter, public TO b46_adapter_runtime;
GRANT SELECT, INSERT ON b46_adapter.inventory_commit_receipts TO b46_adapter_runtime;
GRANT SELECT, UPDATE ON public."Items" TO b46_adapter_runtime;
GRANT SELECT ON public."CompositeItems" TO b46_adapter_runtime;
GRANT INSERT ON public."StockMovements" TO b46_adapter_runtime;
GRANT SELECT ("Id") ON public."Users" TO b46_adapter_runtime;
```

Do not grant access to POS sales, invoices, payments, shifts, settings, or
Ordering tables. Provision the movement actor separately in the POS database;
the adapter migration never writes POS-owned `public` records.

## Compatibility and health

Compatibility was inspected at POS revision `369997c`. Readiness checks UUID
identities, integer stock/movement quantity, boolean active/tracked/composite
flags, numeric composite quantity, timezone-aware timestamps, the receipt
migration, and the actor. `/health/live` reports process liveness only;
`/health/ready` returns 503 with a generic code when any dependency is invalid.

When the POS schema changes, update the disposable fixture and compatibility
note in the same reviewed change. Never relax readiness to start an
incompatible deployment.

## Migration and rollback

Apply migrations before starting a new adapter version. Applying `up` twice is
safe. Production uses forward-only corrective migrations; `Down` is only for a
disposable local/test reset. Rolling back application code is safe because
completed receipts are immutable and replayable. Never delete receipts or
reverse stock merely to roll back a deployment.

## Service-token rotation

The adapter accepts one token at a time. To rotate safely:

1. pause or drain the Ordering worker;
2. generate a new random token outside source control;
3. restart the adapter with the new token;
4. restart Ordering with the same token and confirm readiness;
5. resume the worker and verify pending operations drain; and
6. revoke the old value in the deployment secret store.

Orders remain submitted while paused or unauthorized; no stock is lost. Never
put either token in logs, screenshots, issues, or Git.

## Retry runbook

| Safe code | Operator action |
| --- | --- |
| `INVENTORY_UNREACHABLE` | Check network, process, and liveness. |
| `INVENTORY_TIMEOUT` | Check latency/locks; retry is safe after an ambiguous commit. |
| `INVENTORY_THROTTLED` | Reduce load or restore capacity. |
| `INVENTORY_UNAUTHORIZED` | Repair coordinated token configuration. |
| `INVENTORY_SERVER_ERROR` | Inspect adapter logs by operation/request ID. |
| `INVENTORY_CONTRACT_REJECTED` | Check versions and product UUIDs. |
| `INVENTORY_OPERATION_CONFLICT` | Investigate contradictory operation reuse. |
| `INVENTORY_INVALID_RESPONSE` | Treat the deployment as incompatible. |

Never replay with a new `operation_id`. Repair the cause and let Ordering retry
the original immutable operation.

## Verification

`node scripts/check-phase4.mjs` runs contracts, migrations, architecture, both
Go modules, database integration, concurrency, timeout replay, and the real
Ordering-to-adapter lifecycle when both test URLs are set. Destructive fixtures
refuse to run unless the store database name ends in `_test`.

The adapter writes the existing POS inventory tables directly through its own
transaction boundary. No POS application source change or companion POS
deployment is part of this integration.
