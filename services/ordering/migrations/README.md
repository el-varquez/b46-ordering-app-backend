# Ordering migrations

Goose v3 runs these SQL migrations in filename order. Create the next migration
with a five-digit prefix, for example `00002_identity_indexes.sql`. Once a
migration has reached any shared environment it is immutable; corrections are
new forward migrations.

Use `go run ./cmd/migrate -dir migrations up` and `status` from
`services/ordering`. `DATABASE_URL` is always required. Rollbacks are reserved
for disposable developer/test databases because production changes are forward-only.
