# B46 Ordering Backend

Go modular monolith for B46 Ordering and its isolated inventory adapter.
It provides a POS-backed, quantity-free mobile catalog,
idempotent checkout, the transactional outbox, Customer order status, the
Cashier queue, and the Preparing to Delivering to Delivered lifecycle. Password
and Google identity use opaque mobile sessions with rotating refresh tokens.
Customer email registration verifies a one-time code before creating an account.

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
The API exposes health, identity, catalog, Customer-order, Cashier-order, and
Admin Cashier-management routes on
port 8080. Protected routes use
`Authorization: Bearer <access-token>`; refresh tokens are accepted only by the
refresh operation's JSON body. No browser cookie authentication is used.

Run `make help` for all commands. `make check` is the single completed backend gate. Set
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

If the Admin loses their password, an operator with access to this backend's
private environment can recover that **existing** Admin; there is no public
recovery endpoint and the command cannot create another Admin. Confirm the
configured `DATABASE_URL` names the intended Ordering database first. Then
run `make recover-admin` with `B46_RECOVER_ADMIN_EMAIL` and
`B46_RECOVER_ADMIN_PASSWORD` supplied only in the current shell. The command
replaces the password, revokes prior sessions, and records an audit event.
Remove both shell variables immediately afterward. Do not commit the new
password or pass it as a command-line argument.

## Cashier management

Run `make migrate` on an existing development database before starting the
Phase 7 API. An authenticated Admin can list Cashiers, add one with a stable
  temporary password, then send a six-digit email challenge to the Cashier via
  `/v1/admin/cashiers/registrations`. The Cashier gives that code to the Admin,
  whose verification creates or unlocks the account. A pending or legacy
  unverified Cashier cannot sign in; existing unverified accounts require this
  same flow. The Admin can disable or restore a verified Cashier and reset a
  Cashier login under `/v1/admin/cashiers`. The Admin must share a temporary
  password privately; the API never returns it. The Cashier signs in through
the shared email login, then changes it at `POST /v1/me/password` before
Cashier order access is authorized. Disable and reset revoke active sessions.
Repeated status transitions have no duplicate audit effect. Customer or
Cashier credentials cannot call Admin routes. Admin accounts cannot be
modified by the Cashier-management routes.

## OAuth development configuration

Set `GOOGLE_CLIENT_IDS` to the comma-separated mobile/Web audiences outside
version control. Development may leave it empty; Google login then fails
closed. Production requires a Google audience. New Apple login/link intents
are disabled, but historical Apple identity rows are retained. Audit and
recover Apple-only accounts before deploying that change to an existing store.
The regular test suite signs local fixtures and never contacts live providers.

## Customer email registration (SMTP)

Set a stable random `REGISTRATION_CODE_KEY` of at least 32 bytes in this
repository's ignored `.env`; changing it invalidates pending codes. Configure
`SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM`, `SMTP_USERNAME`, and `SMTP_PASSWORD` for
an authenticated STARTTLS sender. For development with a dedicated Gmail
account, use `smtp.gmail.com` on port 587, set `SMTP_FROM` and `SMTP_USERNAME`
to that Gmail address, and use a Google app password as `SMTP_PASSWORD`.
The ordinary account password must not be used. This requires an outbound
connection to port 587; a network that blocks the port cannot send through
Gmail SMTP even with valid credentials.

Maddy remains an option for local mailbox tests. Its `b46.test` domain is
local-only; direct delivery from a residential/mobile IP is not a reliable
way to reach public mailboxes. `SMTP_ALLOW_INSECURE_LOCAL=true` is permitted
only with a loopback-only development sink. Production rejects insecure SMTP
and missing mail settings. Never put SMTP credentials or the code key in
Flutter or committed files.

`POST /v1/auth/password/registrations` accepts name, email, and password and
returns a generic attempt ID. `resend` replaces the six-digit code after a
60-second cooldown; `verify` creates a **Customer** and returns the existing
B46 access/refresh session. Codes expire after 10 minutes and stop after five
wrong attempts. Requests are limited to five per normalized email and 100 per
remote IP per hour. A bounded in-process handoff keeps SMTP latency out of the
public response; it is not a durable mail outbox. A mail delivery failure or
process stop can leave the attempt pending without a delivered code, so the
user can retry resend later. Failure logs omit the recipient and code. A
`202` response means the attempt was queued, **not** that the recipient inbox
received it. If SMTP is not configured, registration returns
`EMAIL_UNAVAILABLE` for every address. The public acknowledgement
intentionally does not reveal whether an email is
already owned. Behind a proxy, the API currently uses the direct peer IP for
the shared limit; configure gateway-level per-client abuse controls as well.

Password recovery is not included in this slice and remains a release gate.

**Open SMTP acceptance gate:** Gmail delivery has not passed an end-to-end
send/receive check. Before treating email registration as ready, confirm the
runtime network can reach `smtp.gmail.com:587`, configure a stable
`REGISTRATION_CODE_KEY` and a Gmail app password without display spaces in the
ignored `.env`, then register with a fresh address. Confirm the message appears
in the sender's Sent folder and the recipient's inbox, and that its code
verifies successfully. Do not infer delivery from HTTP `202` alone. The usual
Wi-Fi previously blocked port 587; test on a network where that port is open.

## Repository map

- `services/ordering`: Ordering composition root, identity, catalog snapshots, order lifecycle, and workers.
- `services/inventory-adapter`: private POS catalog reader and atomic inventory committer.
- `contracts/http`: source-of-truth OpenAPI contract.
- `contracts/inventory/v1`: versioned Ordering/adapter JSON schemas and fixtures.
- `architecture`: dependency lock and architecture-checker self-test.
- `scripts`: the same gates used locally and in CI.
- Shared `b46-ordering-app/docs`: architecture, development, and planning guidance outside this repository.

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

See `b46-ordering-app/docs/architecture/backend.md` and
`b46-ordering-app/docs/backend-development.md` in the shared planning
workspace, plus [contracts](contracts/README.md), before changing lifecycle
code.
