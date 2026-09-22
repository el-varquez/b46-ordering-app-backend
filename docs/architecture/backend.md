# Backend architecture

The backend is a modular monolith with feature-first Clean Architecture. A
module owns domain rules and use cases behind small ports; platform packages own
process-wide mechanics. Only `cmd` composition roots construct concrete
dependencies.

```text
cmd/api -> feature transports/applications/ports <- adapters
       \-> platform configuration, HTTP, logging, PostgreSQL
```

Dependency direction is enforced by `scripts/check-architecture.mjs`:

- domain imports no application, port, adapter, transport, or platform package;
- application depends on domain and ports, never concrete infrastructure;
- transports cannot import PostgreSQL adapters;
- adapters implement ports and do not decide business transitions;
- features cannot import another feature's adapters or transports; and
- Ordering and inventory-adapter implementations never import one another.

The Phase 1 runtime has one intentional seam: HTTP readiness depends on a
`HealthChecker`, implemented by the PostgreSQL adapter. Liveness is process-only.
Future clock, ID, transaction, repositories, and service ports are introduced
with the use cases that need them, instead of as unused abstractions.

The inventory adapter is a separate deployable Go module. Ordering reaches it
only through the private versioned HTTP contract and its `InventoryCommitter`
port; neither module imports the other's implementation. The adapter owns one
deep atomic commit interface that hides POS resolution, locking, deductions,
movements, durable receipts, and exact replay.
