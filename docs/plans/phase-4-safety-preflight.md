# Phase 4 Store Database Safety Preflight

Status: decisions locked; no store data or schema was changed by this
preflight.

This record is the safety gate for the real inventory adapter. It was prepared
from the B46 backend branch `feat/phase-4-real-inventory` and the POS backend at
commit `369997c`.

## Locked integration contract

| Concern | Decision |
| --- | --- |
| Store database | PostgreSQL `pos_db` in retained environments. |
| Product identity | Inventory `product_id` is `public."Items"."Id"`, encoded as a UUID string. Names, item codes, and barcodes are not identities. |
| Receipt ownership | Ordering creates and owns `operation_id`; the adapter stores it as a unique external receipt. |
| Adapter schema | The adapter may create and migrate only the `b46_adapter` schema inside `pos_db`. POS-owned `public` tables remain owned by the POS repository. |
| Negative stock | Online checkout rejects insufficient tracked stock even when another POS setting allows negative stock. |
| Non-stock items | Active items with `TracksStock=false` are orderable and create no stock movement. |
| Composite quantity | Component demand is `ceil(component.Quantity * orderedQuantity)`, matching the inspected POS ledger. |
| Movement type | Adapter deductions use `StockMovementType.Sale`, whose inspected integer value is `1`. |
| POS scope | The adapter updates item stock and writes stock movements only. It does not create POS sales, invoices, payments, shifts, or receipt numbers. |

## Disposable database boundary

Phase 4 automated tests and local resets use a dedicated Compose
service with these fixed identifiers:

| Setting | Value |
| --- | --- |
| Compose service | `store-postgres` |
| Database | `b46_store_test` |
| Host port | `5434` |
| Environment variable | `STORE_TEST_DATABASE_URL` |
| Named volume | `b46-store-postgres-data` |

The local URL shape is:

```text
postgres://b46_adapter_test@localhost:5434/b46_store_test?sslmode=disable
```

The disposable local container uses host trust authentication only. Retained
environments still require runtime-managed credentials. The existing Ordering
database on port `5433` and retained POS database on port
`5432` are outside reset/test scope. A reset command must target only the
explicit `store-postgres` service and `b46-store-postgres-data` volume.

Any future rehearsal using retained data must use a verified backup restored
under a different database name. Tests never run against the source database.

## Dedicated movement actor

The adapter uses this stable actor identifier in all online-order stock
movements:

```text
b4600000-0000-4000-8000-000000000046
```

The disposable fixture seeds a non-login user named `B46 Online Ordering`
with that identifier. Before a retained environment enables the adapter, an
approved database provisioning step must create the same dedicated actor in
`public."Users"`. The adapter migration must not insert it because the adapter
does not own the POS `public` schema.

The adapter receives the UUID through `POS_MOVEMENT_ACTOR_ID` and readiness
fails closed when the actor is absent. It does not borrow a human Admin or
Cashier identity.

## Verified POS schema assumptions

The adapter relies on these inspected records:

| Record | Required columns/meaning |
| --- | --- |
| `public."Items"` | `Id uuid`, `Stock integer`, `IsActive boolean`, `TracksStock boolean`, `IsComposite boolean`, `UpdatedAt timestamptz` |
| `public."CompositeItems"` | `ParentItemId uuid`, `ComponentItemId uuid`, `Quantity numeric(18,3)` |
| `public."StockMovements"` | UUID identity/item, integer type, signed integer quantity, notes, actor UUID, timestamps |
| `public."Users"` | dedicated movement actor UUID |

Compatibility and readiness checks must fail closed if these assumptions no
longer hold. The expected POS revision is documented and deliberately updated
when a compatible POS schema change is reviewed.

## POS integration boundary

The adapter integrates by writing the existing POS inventory tables directly.
The POS application source is not modified and no companion POS branch or
deployment is required. All integration-specific behavior remains behind the
adapter's inventory commit interface.

For each Ordering operation, the adapter:

1. opens one PostgreSQL transaction in the POS database;
2. expands composite demand and locks affected item UUIDs in ascending order;
3. reads the current item state and rejects inactive, missing, or insufficient
   tracked stock;
4. conditionally deducts every tracked item;
5. inserts the existing POS stock-movement records;
6. stores the adapter-owned receipt keyed by Ordering's `operation_id`; and
7. commits all deductions, movements, and the receipt together.

If any item is unavailable, the entire transaction rolls back and the adapter
returns the unavailable items to Ordering for customer-facing handling. A
retry with the same immutable operation is idempotent; contradictory reuse of
an operation identifier is rejected.

## Database roles

Use different credentials for migration and runtime.

The migration role may create and migrate `b46_adapter`. The runtime role is
limited to:

- `USAGE` on `b46_adapter`;
- `SELECT` and `INSERT` on adapter receipt tables;
- `SELECT` and `UPDATE` on required `public."Items"` rows;
- `SELECT` on `public."CompositeItems"`;
- `INSERT` on `public."StockMovements"`; and
- minimal `SELECT` on `public."Users"` to validate the configured actor.

It receives no Ordering database access and no general POS access to users,
payments, shifts, sales, invoices, or settings.

## Checkpoint answers

1. The exact disposable database is `b46_store_test` in `store-postgres` on
   host port `5434`.
2. `b46_adapter` is approved as the adapter-owned schema inside `pos_db`.
3. The movement actor UUID is
   `b4600000-0000-4000-8000-000000000046`.
4. `product_id` is the UUID value of `public."Items"."Id"`.
5. The adapter performs sorted, conditional multi-item deductions in one
   transaction while leaving the POS application source untouched.

## Preflight result

The direct-table adapter design and local verification boundary are complete.
Retained-database integration remains disabled until the dedicated actor and
least-privilege adapter database roles are provisioned.
