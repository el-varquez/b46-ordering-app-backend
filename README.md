# B46 Ordering Backend

Initial Clean Architecture scaffold for the B46 ordering system.

- `services/ordering`: public Ordering backend.
- `services/inventory-adapter`: private adapter for the store inventory database.
- `contracts`: versioned public and private wire contracts.
- `database`: Ordering database support files.
- `tests`: cross-module contract and integration checks.

The directories intentionally contain no implementation yet. Development starts
with Phase 1 after this scaffold is committed.
