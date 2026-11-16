# Frontend rendering and component decomposition

## Rendering strategy

Use Next.js App Router for the application shell and client-side navigation,
so moving between routes does not reload the whole document. Client-side
rendering (CSR) powers API-driven data and interactive workflows. Use
server-side rendering (SSR) for protected-route session checks and the
personalized initial database list; use static site generation (SSG) with
incremental static regeneration (ISR) only for public, stable landing content.
Never cache a response containing user or session data in a shared cache.

| Route                 | Initial response                                                            | Client behavior                                                                                  | Reason                                                                 |
| --------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------- |
| `/`                   | SSG, revalidated with ISR about once per hour                               | Optional auth-aware call to action; do not personalize the cached HTML                           | Public feature overview                                                |
| `/login`              | SSG shell                                                                   | CSR form submission, validation feedback, and safe `next` redirect handling                      | No user-specific page data                                             |
| `/register`           | SSG shell                                                                   | CSR form submission and validation feedback                                                      | No user-specific page data                                             |
| `/admin`              | SSR session/admin-mode guard and shell                                      | CSR mode switch and navigation                                                                   | Do not render admin content before authorization                       |
| `/admin/connections`  | SSR admin guard and shell                                                   | CSR connection list, health/pool updates, and create/edit/test/connect/disconnect/delete actions | Frequently changing data and mutations                                 |
| `/admin/groups`       | SSR admin guard and shell                                                   | CSR group list, member picker, leader selection, and group mutations                             | Frequently changing data and mutations                                 |
| `/admin/access`       | SSR admin guard and shell                                                   | CSR access matrix and grant/revoke actions                                                       | Access changes must take effect promptly                               |
| `/db`                 | SSR session guard and initial accessible-database list                      | CSR revalidation and status/review indicators                                                    | Fast first paint for private, per-user data                            |
| `/db/{id}`            | SSR session guard and database shell; conceal unauthorized IDs as not found | CSR schema, query, and migration tabs; paginate large results                                    | Large, interactive datasets and workflows                              |
| Not found / forbidden | Static shell where possible; use SSR when the response depends on a guard   | Client navigation uses the same app shell                                                        | Keep error pages simple and avoid leaking protected resource existence |

`/db/{id}` tab selection may use a query parameter or hash, as specified.
Internal links use Next.js client-side navigation; direct loads and refreshes
must still render the route and run its server guard. The Migration tab is
available only for PostgreSQL connections. Keep permission checks authoritative
on the backend; client-side visibility is only a UX aid.

### Next.js implementation notes

Build the route tree and shared layouts with the Next.js App Router. Keep
protected page guards and any initial personalized data fetch on the server;
put interactive API-driven features in client components. Configure ISR for the
public landing page only. This is a single-page application in navigation and
interaction, not a requirement to send every route as an empty client-only
shell: SSR remains useful for guarded direct loads and the initial `/db`
response.

## Reusable component decomposition

Components marked **SSR shell** render useful semantic HTML on the server.
Components marked **CSR island** fetch or mutate API data in the browser and
provide loading, empty, error-with-retry/request-ID, and success states.
Presentational components can be rendered on either side when they need no
browser-only state.

| Component                                      | Rendering                           | Reuse / responsibility                                                                                            |
| ---------------------------------------------- | ----------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `AppLayout` / `PublicLayout`                   | SSR or SSG                          | Document metadata, global styles, responsive page frame, and public navigation                                    |
| `AuthenticatedLayout`                          | SSR shell                           | Authenticated navigation and account controls; show only links allowed by `/api/me`                               |
| `AdminLayout`                                  | SSR shell                           | Admin navigation and mode indicator; only render after the server-side admin guard                                |
| `PageHeader` / `Breadcrumbs`                   | SSR                                 | Consistent page title, context, and navigation                                                                    |
| `LoadingState`, `EmptyState`, `ErrorState`     | SSR + CSR                           | Shared accessible async states; errors include retry and request ID where available                               |
| `ToastRegion`, `ConfirmDialog`                 | CSR island                          | Mutation feedback and destructive-action confirmation                                                             |
| `StatusBadge`, `AccessBadge`                   | SSR + CSR                           | Consistent rendering for connection, request, job, migration, and access states                                   |
| `DataTable`, `PaginationControls`              | SSR + CSR                           | Accessible responsive tables, sorting, pagination, and mobile overflow                                            |
| `LoginForm`, `RegisterForm`                    | CSR island in SSG shell             | Auth requests, field validation, session-expired and rate-limit messages                                          |
| `AdminModeSwitcher`                            | CSR island                          | Switch between user and admin mode for eligible users                                                             |
| `ConnectionList` / `ConnectionCard`            | CSR island in admin SSR shell       | Connection status, engine, pool stats, and connection actions                                                     |
| `ConnectionForm`                               | CSR island in admin SSR shell       | Shared create/edit fields, test-before-save, and write-only password behavior                                     |
| `GroupList`, `GroupEditor`, `MemberPicker`     | CSR island in admin SSR shell       | Group membership, exactly-one-leader selection, and group lifecycle actions                                       |
| `AccessGrantMatrix`                            | CSR island in admin SSR shell       | Grant, change, and revoke group access with impact warnings                                                       |
| `DatabaseList` / `DatabaseCard`                | SSR initial data + CSR revalidation | Accessible database list, access level, connection state, and review/job indicators                               |
| `DatabaseWorkspaceShell`, `DatabaseTabs`       | SSR shell + CSR navigation          | Database title, tabs, active selection, and engine-aware tab availability                                         |
| `SchemaTree`                                   | CSR island                          | Schema/table navigation, including the single-level MySQL/MariaDB case                                            |
| `TableDetailsTabs`, `TableDataGrid`            | CSR island                          | Columns, constraints, indices, and paginated/sortable table data                                                  |
| `SqlEditor`, `QueryResults`                    | CSR island                          | Read-only query execution, cancellation, row limits, execution time, and inline errors                            |
| `MigrationHistory`                             | CSR island                          | Migration history and paired rollback visibility toggle                                                           |
| `DraftList`, `DraftEditor`, `ValidationReport` | CSR island                          | Draft lifecycle, validation/rebase, immutable/stale states, and duplicate-as-new                                  |
| `RequestList`, `RequestReview`                 | CSR island                          | Request details, per-group approval progress, and approve/reject actions                                          |
| `JobList`, `JobProgress`                       | CSR island                          | Job and batch progress; poll every 2 seconds with backoff to 10 seconds, stopping on terminal state or hidden tab |

Keep components focused: `ConnectionForm` should not own connection-list
loading, and `DatabaseWorkspaceShell` should not fetch every schema, table,
query result, and migration record before the user opens a tab.

### Implemented component modules

The reusable components are grouped under `src/components/`:

- `shared/`: layouts, page headers and breadcrumbs, async states, status/access
  badges, generic tables and pagination, confirmation dialog, and toast region.
- `auth/`: login and registration form UI with client-side validation.
- `admin/`: connection, group/member, access-grant, and admin-mode controls.
- `database/`: database cards/lists, workspace navigation, schema tree, table
  data controls, SQL editor, and query results.
- `migration/`: history, draft editor/list, request review/list, and job status
  components.
- `index.ts`: component and type exports for convenient imports.

These are typed UI components with callbacks for data operations. Wire those
callbacks to the app's API/query layer; no component assumes a backend URL or
silently substitutes mock data. Protected-route authorization remains the
responsibility of the server route/layout, not these display components.

## Pages and mock services

The App Router pages are implemented in `src/app/` for the landing page,
login/register, admin home/connections/groups/access, the database list, and
database workspaces. The public landing content uses Next.js Cache Components
with a one-hour revalidation lifetime. Protected route layouts call the mock
session service so the routes can be explored before the Go API is connected.

`src/lib/mock-data.ts` supplies typed in-memory data. Its generic
`MockRepository<T>` base class is extended by connection, group, database,
access-grant, migration, and user repositories; `MockAuthService` supplies a
demo administrator. The pages pass mock data to the reusable components, and
client controls demonstrate local state changes and notifications.

The demo-mode banner is intentional: login/register, permissions, and
connection/migration actions are not real authentication or database
operations. Replace the mock services and client callbacks with the API layer
before deployment; the mock administrator must not be used as an authorization
guard in production.

## Page composition

| Page                  | Compose from                                                                                                              |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `/`                   | `PublicLayout`, `PageHeader`, public feature sections                                                                     |
| `/login`, `/register` | `PublicLayout`, `LoginForm` or `RegisterForm`                                                                             |
| `/admin`              | `AdminLayout`, `AdminModeSwitcher`, admin destination cards                                                               |
| `/admin/connections`  | `AdminLayout`, `ConnectionList`, `ConnectionForm`, shared status and feedback components                                  |
| `/admin/groups`       | `AdminLayout`, `GroupList`, `GroupEditor`, `MemberPicker`                                                                 |
| `/admin/access`       | `AdminLayout`, `AccessGrantMatrix`                                                                                        |
| `/db`                 | `AuthenticatedLayout`, `DatabaseList`, `DatabaseCard`                                                                     |
| `/db/{id}`            | `AuthenticatedLayout`, `DatabaseWorkspaceShell`, `DatabaseTabs`, then the selected schema, query, or migration components |

This is a fresh Next.js implementation plan, not a migration contract for the
Astro source tree. Follow the spec routes: user search belongs in the
group-member picker, rather than a separate `/admin/users` page.

## Cross-cutting behavior

- Every protected request is guarded on the server. Unauthenticated users go
  to `/login?next=...`; unauthorized admin routes return a forbidden response.
- Send the CSRF token on unsafe API methods. Handle `401 SESSION_EXPIRED`
  globally and preserve a safe same-origin return path.
- Backend permissions are authoritative. Never treat a hidden or disabled
  control as authorization.
- Mutations show pending state, disable duplicate submission, and report
  success or API errors. Do not silently fall back when an API request fails.
- Personalized HTML and API responses must be private and not shared-cacheable.
- Use semantic controls, visible keyboard focus, labeled inputs, and layouts
  usable from 360 px wide.
