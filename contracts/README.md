# Contract ownership and identifier semantics

This repository is the source of truth for the public HTTP contract and the
private Ordering-to-inventory-adapter contract. Consumers pin an immutable tag
or commit of this repository; they do not copy Go source into another project.

`http/openapi.json` includes identity plus the Phase 3 Customer checkout and
history operations and Cashier queue, detail, read, and status operations.
Customer schemas intentionally omit internal order states, inventory
quantities, operation/event IDs, outbox state, claims, and retry metadata.

## Stable vocabulary

- Roles: `CUSTOMER`, `CASHIER`, `ADMIN`.
- Internal order states: `SUBMITTED`, `CONFIRMED`, `REJECTED`.
- Fulfillment states: `PREPARING`, `DELIVERING`, `DELIVERED`.
- Customer order states: `PREPARING`, `ON_THE_WAY` for internal `DELIVERING`,
  `DELIVERED`, and `REJECTED`.

`New`, outbox status, migration status, and adapter processing status are not
customer-visible order states.

## Identifiers and guarantees

| Identifier | Owner and meaning | Idempotency/retry rule |
| --- | --- | --- |
| `checkout_id` | Client-generated ID for one logical Place Order attempt | Reusing it returns the same logical order outcome; it never creates a second order. |
| `order_id` | Ordering-owned immutable order identity | Used across order lines, fulfillment, outbox, and inventory results. |
| `order_line_id` | Ordering-owned immutable line identity | Lets an unavailable result identify the exact submitted line. |
| `event_id` | Producer-owned immutable event identity | A consumer records/deduplicates each event once even when delivery is retried. |
| `operation_id` | Ordering-owned identity for the whole order inventory deduction | All order items share one operation. The adapter returns one final `COMMITTED` or `ITEMS_UNAVAILABLE` result, and replay returns that result without deducting twice. |

Ordering creates the order, inventory operation, and outbox row in one database
transaction in a later phase. The adapter applies all items in one store-side
transaction. Events may be delivered more than once and consumers must be
idempotent. Per-operation results are final and cannot change after completion.

## Versioning

Inventory schema version `1.0` is frozen under `contracts/inventory/v1`.
Compatible additions require optional fields. Breaking changes require a new
version directory. Every example is checked against its schema by
`node scripts/check-contracts.mjs` and decoded strictly by Go tests.
