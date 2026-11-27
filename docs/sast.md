# SAST findings

This document records the disposition of the CodeQL SQL-injection findings
tracked by [#14](https://github.com/SlackingSlothh/hello-world/issues/14)
through [#18](https://github.com/SlackingSlothh/hello-world/issues/18).
The row-list findings have code mitigations. The raw-SQL findings remain
intentional input flows; their presence here does not mean CodeQL has been
rerun or that those alerts have been suppressed.

## Finding 1 — MySQL row ordering ([#14](https://github.com/SlackingSlothh/hello-world/issues/14))

- **Affected area:** The `column` and `sort` URL parameters flow from the HTTP
  rows handler through the catalog service to the MySQL catalog reader, where
  the sort identifier is used in the generated `ORDER BY` clause.
- **Disposition:** Mitigated in code.
- **Solution:** Filter, sort, and selected-column names are checked against
  catalog metadata. The query now uses the matched metadata column name and
  quotes it with the MySQL identifier-quoting helper. Filter values and page
  bounds remain SQL parameters. Unknown column names are rejected.
- **Relevant code:** `backend/internal/transport/http/handler/handler.go`,
  `backend/internal/services/catalog/service.go`, and
  `backend/internal/database/target/mysql/catalog.go`.

## Finding 2 — PostgreSQL row ordering ([#15](https://github.com/SlackingSlothh/hello-world/issues/15))

- **Affected area:** The same HTTP rows parameters flow through the catalog
  service to the PostgreSQL catalog reader and its generated `ORDER BY`
  clause.
- **Disposition:** Mitigated in code.
- **Solution:** Filter, sort, and selected-column names are checked against
  catalog metadata. The query uses the matched metadata column name and quotes
  it with the PostgreSQL identifier-quoting helper. Filter values and page
  bounds use PostgreSQL bind parameters. Unknown column names are rejected.
- **Relevant code:** `backend/internal/transport/http/handler/handler.go`,
  `backend/internal/services/catalog/service.go`, and
  `backend/internal/database/target/postgres/catalog.go`.

## Finding 3 — Migration SQL passed to EXPLAIN ([#16](https://github.com/SlackingSlothh/hello-world/issues/16))

- **Affected area:** The migration explain HTTP request supplies `SQLContent`,
  which flows through `migration.Service.ExplainQuery` to
  `sqlTargetTx.ExplainSQL`. The connector prefixes the submitted statement
  with the dialect's `EXPLAIN` syntax.
- **Disposition:** Not redesigned; retained as an intentional SQL input
  feature.
- **Reason:** The migration editor must explain the SQL statement submitted by
  an authorized migration user. SQL text cannot be passed as a bind value to
  `EXPLAIN`; doing so would explain a string value rather than that statement.
  The service validates transactional SQL and enforces a single statement
  before execution. `EXPLAIN ANALYZE` is further restricted to `SELECT` for
  MySQL/MariaDB, and the target transaction is rolled back after planning.
  Replacing this flow would change the migration-explain feature.
- **Relevant code:** `backend/internal/transport/http/handler/migration.go`,
  `backend/internal/services/migration/service.go`, and
  `backend/internal/database/target/migration/migration_connector.go`.

## Finding 4 — MySQL custom read query ([#17](https://github.com/SlackingSlothh/hello-world/issues/17))

- **Affected area:** The query HTTP request supplies `req.SQL`, which flows
  through the catalog service to `mysql.Reader.Select` and its
  `tx.QueryContext` call.
- **Disposition:** Not redesigned; retained as an intentional SQL input
  feature.
- **Reason:** The query endpoint is a custom SQL query feature. The service
  applies `ValidateSelect` (single `SELECT`, rejects disallowed operation
  tokens and access to the internal schema), and the MySQL reader executes it
  in a read-only transaction. `req.Args` are passed separately as bind
  parameters, but binding values cannot make arbitrary user-authored SQL text
  safe without removing the custom-query feature. Replacing it with a
  structured query API would change the endpoint's behavior.
- **Relevant code:** `backend/internal/transport/http/handler/handler.go`,
  `backend/internal/services/catalog/query_validation.go`,
  `backend/internal/services/catalog/service.go`, and
  `backend/internal/database/target/mysql/catalog.go`.

## Finding 5 — PostgreSQL custom read query ([#18](https://github.com/SlackingSlothh/hello-world/issues/18))

- **Affected area:** The same query HTTP request supplies `req.SQL`, which
  flows through the catalog service to `postgres.Reader.Select` and its
  `tx.QueryContext` call.
- **Disposition:** Not redesigned; retained as an intentional SQL input
  feature.
- **Reason:** The endpoint intentionally executes custom SQL queries.
  `ValidateSelect` restricts the accepted statement and internal-schema access,
  and the PostgreSQL reader uses a read-only transaction. `req.Args` are
  separate bind parameters, but they do not parameterize the SQL statement
  itself. Removing this flow requires replacing the custom-query API with a
  structured query interface, changing existing behavior.
- **Relevant code:** `backend/internal/transport/http/handler/handler.go`,
  `backend/internal/services/catalog/query_validation.go`,
  `backend/internal/services/catalog/service.go`, and
  `backend/internal/database/target/postgres/catalog.go`.