# Backend

See the [root README](../README.md) to run the stack. The backend uses Go 1.26.
Treat [OpenAPI](api/openapi.yaml) as the reference for routes, permissions, and payloads.

## Code layout

- `cmd/app` is the entry point. `internal/app` wires dependencies, applies platform schema changes, and seeds local accounts.
- `internal/transport/http` handles HTTP. `internal/services` owns business rules and dependency interfaces.
- `internal/database/platform` persists accounts, grants, connections, and migration history in the platform PostgreSQL database. Add platform schema changes as numbered, single-statement files under [`migrations/`](migrations/).
- `internal/database/target` manages user databases and executes target SQL. Supported engines are PostgreSQL, MySQL, and MariaDB.

## Tests

Run from `backend/`.

```sh
go test ./...
```

Set `TEST_DATABASE_DSN` to a disposable PostgreSQL database to include integration
tests. The test role needs permission to create schemas.

## Reconcile a PostgreSQL migration

If an attempt is stuck in `RUNNING`, a platform admin can send
`POST /api/v1/admin/connections/{id}/migration-runs/{runID}/reconcile` with an
authenticated session and the app's configured origin. Use the execution ID from
migration history. A confirmed outcome updates the attempt to `SUCCEEDED` or
`FAILED` without rerunning SQL.

Evidence collection requires PostgreSQL 13 or later. If needed, grant the target
connection role access before running migrations.

```sql
GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO migration_role;
```

HTTP `409` leaves the connection reserved when evidence is unavailable.
Evidence can expire or become unusable after a target change, restart, or reset of
its background-writer statistics. A reconciled attempt's finish time records when
the platform saved the result.
