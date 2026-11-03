# Backend v2 structure

This directory contains the active backend implementation. The structure
makes the three main layers easy to see:

- **Transport** translates HTTP requests and responses.
- **Services** implement business rules and use cases.
- **Database** implements persistence and target-database operations.

`cmd/app` assembles these layers and starts the server. `migrations` contains
the SQL scripts for the application's internal/platform database. The former
`backend/` implementation has been moved here and reorganized by layer. The
API contract is kept in `api/openapi.yaml`.

## Layout

```text
backend_v2/
├── api/
│   └── openapi.yaml
├── cmd/
│   └── app/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── app.go                 # lifecycle, dependency wiring, shutdown
│   │   └── seed.go                # bootstrap users/groups
│   ├── config/
│   │   └── config.go              # environment/config parsing and validation
│   ├── database/
│   │   ├── platform/              # internal PostgreSQL repositories
│   │   └── target/                # target pools, dialects, migration connector
│   ├── models/
│   │   └── catalog/               # schema/table/column/query result types
│   ├── services/
│   │   ├── access/
│   │   ├── authn/
│   │   ├── authz/
│   │   ├── catalog/
│   │   ├── connection/
│   │   ├── migration/
│   │   ├── session/
│   │   └── users/
│   └── transport/
│       └── http/
│           ├── handler/
│           ├── httpapi/
│           └── server/
├── migrations/
│   ├── runner.go                  # applies these embedded scripts
│   ├── 0001_....sql
│   └── ...
├── go.mod
└── README.md
```

Keep packages organized by feature **inside** each layer. Avoid a single large
`models` package or generic files such as `service.go` and `handler.go` that
accumulate unrelated features.

## Layer responsibilities and dependency direction

```text
cmd/app ──> internal/app ──> config
                   ├──────> database ──> models
                   ├──────> services ──> models
                   └──────> transport/http ──> services
```

The composition root (`internal/app`) constructs concrete database adapters,
services, HTTP handlers, and the server. At runtime, handlers call service
interfaces and services call their persistence/target ports. Database
implementations satisfy those ports structurally; they do not need to import
the service packages.

1. **`transport/http`**
   - Owns routes, HTTP middleware, decoding/validation specific to the wire
     format, and status-code/response mapping.
   - Does not run SQL or contain business rules.
   - Keep session and permission checks at the transport boundary; services
     still enforce authorization for sensitive use cases as defense in depth.
2. **`services`**
   - Owns use cases, business validation, authorization decisions, and
     interfaces for required storage or target-database behavior.
   - Receives `context.Context` for request cancellation and deadlines.
   - Does not import HTTP, `database/sql`, or a specific SQL dialect.
3. **`database`**
   - Owns `database/sql`, SQL statements, transaction management, database
     driver setup, platform repositories, target pool lifecycle, and dialect
     adapters.
   - Implements the interfaces consumed by services; it does not decide HTTP
     status codes or parse HTTP requests.
   - Keep platform-database code distinct from target-database code even though
     both use SQL.
4. **`models`**
   - Contains domain data types shared between service and adapter code.
   - Should not import transport or database packages. Do not put SQL row
     structs here when they are only implementation details of one repository.
5. **`internal/app` and `cmd/app`**
   - `cmd/app/main.go` should stay small: load configuration, call the app
     constructor, run, and handle process signals.
   - `internal/app` owns initialization order, wiring, startup migration,
     bootstrap data, and graceful shutdown.

Keep service dependency interfaces close to the use case that needs them (or
in a small feature-specific `ports.go`). Concrete database adapters satisfy
those interfaces structurally. This avoids a broad shared `interfaces`
package and prevents a dependency cycle.

## Mapping from the former `backend/`

| Former files/packages | Destination | Notes |
|---|---|---|
| `backend/main.go` | `cmd/app/main.go`, `internal/app/app.go`, and `internal/app/seed.go` | The entry point delegates startup and lifecycle to the composition root. |
| `backend/server/config.go` | `internal/config/config.go` | Configuration loading/validation; no HTTP route registration. |
| `backend/server/server.go` | `internal/transport/http/server/` | HTTP server construction and route mounting. App wiring remains in `internal/app`. |
| `backend/handler/*.go` | `internal/transport/http/handler/` | API routes and handlers, split by access and migration responsibilities. |
| `backend/httpapi/*.go` | `internal/transport/http/httpapi/` | Login/logout handlers, session and authorization middleware, and same-origin protection. |
| `backend/app/connection/*.go` | `internal/services/connection/` | Connection-management use cases. |
| `backend/app/catalog/service.go` | `internal/services/catalog/` | Catalog/query use cases, paging validation, and query restrictions. |
| `backend/app/access/*.go` | `internal/services/access/` | User/group/membership/grant use cases and validation. |
| `backend/migration/service.go` | `internal/services/migration/` | Migration lifecycle and orchestration. |
| `backend/authn/`, `backend/authz/`, `backend/session/`, `backend/users/` | Matching feature packages under `internal/services/` | Authentication, authorization, session lifecycle, and user boundaries remain distinct. |
| `backend/persistence/*.go` | `internal/database/platform/` | Internal PostgreSQL repositories for users, groups, memberships, grants, connections, and sessions. |
| `backend/migration/postgres.go` | `internal/database/platform/migration_repository.go` | PostgreSQL store for migration metadata and run history, not the target connector. |
| `backend/manageddb/*.go` | `internal/database/target/manageddb/` | Runtime target registry, driver allowlist, pool setup, and connection replacement. |
| `backend/migration/target_sql.go` | `internal/database/target/migration/` | Target transaction, lock, explain, schema inspection, and dialect behavior. |
| `backend/dialect/{postgres,mysql}/` | `internal/database/target/{postgres,mysql}/` | Dialect-specific target catalog implementations. |
| `backend/dbops/catalog.go` | `internal/models/catalog/` and the catalog service's reader port | Separates shared catalog data types from the service-owned reader interface. |
| `backend/dbops/query.go` | `internal/services/catalog/query_validation.go` | Read-only query policy is a business/security rule, not SQL persistence. |
| `backend/migrations/` | `backend_v2/migrations/` | Platform schema scripts and their embedding runner remain together. |
| `backend/api/openapi.yaml` | `backend_v2/api/openapi.yaml` | The API contract is kept beside the backend. |
| `backend/**/test` and `*_test.go` | Tests beside their migrated feature packages | Reusable fakes stay in narrowly scoped test-support packages. |

File names and package boundaries are kept where practical; preserve
transaction boundaries and existing HTTP behavior when making later changes.

## Internal database versus target databases

There are two distinct database roles. Keep their adapters and terminology
separate:

- **Internal/platform database:** the application's PostgreSQL database. It
  stores users, sessions, groups/grants, managed connection metadata and
  encrypted DSNs, and migration definitions/runs. Its SQL schema changes are
  the numbered files in `backend_v2/migrations/`.
- **Target database:** a user-managed PostgreSQL, MySQL, or MariaDB database.
  `internal/database/target` owns runtime pools, schema/catalog readers, and
  migration execution transactions. It must not store platform state.

Do not let a target connection repository accidentally use the platform pool,
or let platform migration scripts be sent to a target database.

### Migration runner and embedded SQL

Go's `//go:embed` cannot embed files from a parent directory. If the runner
must embed the scripts while the scripts remain in `backend_v2/migrations`,
the simplest option is to keep `runner.go` in that directory alongside its
numbered `.sql` files. The Go file is the runner; the numbered files remain
the schema scripts. Alternatively, make the runner read an injected `fs.FS`
or a configured external directory, but then startup must guarantee the
correct scripts are present. Choose one mechanism deliberately rather than
copying migration files into a second directory.

## Testing and database verification

Run package tests from this directory with `go test ./...`. Tests requiring a
real PostgreSQL platform database are skipped unless `TEST_DATABASE_DSN` is
configured. Use disposable target databases for SQL dialect, transaction, and
migration execution checks. Compare route permissions, payloads, and status
codes with `api/openapi.yaml` when changing the HTTP surface.

Use fakes for service unit tests and `context.Background()` for normal tests;
use cancellable or deadline contexts only for tests that specifically verify
timeouts/cancellation. Use disposable real databases for SQL dialect,
transaction, and migration-runner integration tests.

Avoid changing API behavior, access rules, error codes, or migration semantics
as part of a folder move. Treat any such behavior change as a separate,
explicitly tested change.
