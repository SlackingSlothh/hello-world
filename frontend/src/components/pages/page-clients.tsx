"use client";

import Link from "next/link";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ConnectionForm,
  ConnectionList,
  GroupAccessEditor,
  type AccessGrant,
  type ConnectionFormValues,
  type ConnectionSummary,
} from "@/components/admin/admin-panels";
import {
  AuthForm,
  type LoginFormValues,
} from "@/components/auth/auth-form";
import {
  DatabaseList,
  DatabaseWorkspaceShell,
  QueryResults,
  SchemaTree,
  SqlEditor,
  TableDataGrid,
  TableDetailsTabs,
  type DatabaseSummary,
  type SchemaNode,
  type TableDetailTab,
  type WorkspaceTab,
} from "@/components/database/database-panels";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/components/shared/async-states";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import { PageHeader } from "@/components/shared/page-header";
import {
  accessGrantLevel,
  createConnection,
  deleteConnection,
  listAdminConnections,
  listGroupAccess,
  setConnectionConnected,
  setGroupConnectionAccess,
  updateConnection,
} from "@/lib/api/admin";
import { login } from "@/lib/api/auth";
import {
  executeDatabaseQuery,
  getDatabaseConnection,
  listDatabaseConnections,
  loadDatabaseColumns,
  loadDatabaseConstraints,
  loadDatabaseIndexes,
  loadDatabaseRows,
  loadDatabaseSchemas,
  type DatabaseColumn,
  type DatabaseConstraint,
  type DatabaseIndex,
  type DatabaseQueryResult,
  type DatabaseRowsPage,
} from "@/lib/api/database";

export function AuthPageForm({
  nextPath,
  sessionExpired = false,
}: {
  nextPath?: string;
  sessionExpired?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(values: LoginFormValues) {
    setBusy(true);
    setError(undefined);
    try {
      await login(values);
      toast.success("Signed in.");
      window.location.replace(safeReturnPath(nextPath));
    } catch (submitError) {
      setError(
        submitError instanceof Error
          ? submitError.message
          : "Could not sign in.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthForm
      error={error}
      onSubmit={submit}
      sessionExpired={sessionExpired}
      submitting={busy}
    />
  );
}

function safeReturnPath(path?: string) {
  if (!path) return "/db";
  try {
    const destination = new URL(path, window.location.origin);
    if (destination.origin === window.location.origin) {
      return `${destination.pathname}${destination.search}${destination.hash}`;
    }
  } catch {
    return "/db";
  }
  return "/db";
}

export function AdminHome() {
  return (
    <div>
      <PageHeader
        description="Manage database connections, groups, and access grants."
        title="Admin"
      />
      <ul className="grid gap-4 md:grid-cols-3">
        {[
          [
            "Connections",
            "Configure managed database connections.",
            "/admin/connections",
          ],
          ["Groups", "Manage group members and leaders.", "/admin/groups"],
          ["Access", "Grant groups view or edit access.", "/admin/access"],
        ].map(([title, description, href]) => (
          <li key={href}>
            <Link
              className="block h-full rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              href={href}
            >
              <Card className="h-full transition-colors hover:bg-muted/40">
                <CardHeader>
                  <CardTitle>{title}</CardTitle>
                  <CardDescription>{description}</CardDescription>
                </CardHeader>
                <CardContent className="text-sm font-medium text-primary">
                  Open {title.toLowerCase()} →
                </CardContent>
              </Card>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

export function ConnectionManagement() {
  const [connections, setConnections] = useState<ConnectionSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const [formOpen, setFormOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [pendingConnectionId, setPendingConnectionId] = useState<string>();
  const [editing, setEditing] = useState<ConnectionSummary>();
  const [deleting, setDeleting] = useState<ConnectionSummary>();

  const loadConnections = useCallback(() => listAdminConnections(), []);

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError(undefined);
    try {
      setConnections(await loadConnections());
    } catch (error) {
      setLoadError(errorMessage(error, "Could not load connections."));
    } finally {
      setLoading(false);
    }
  }, [loadConnections]);

  useEffect(() => {
    let cancelled = false;
    loadConnections()
      .then((items) => {
        if (!cancelled) setConnections(items);
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setLoadError(errorMessage(error, "Could not load connections."));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [loadConnections]);

  async function saveConnection(values: ConnectionFormValues) {
    setBusy(true);
    setActionError(undefined);
    try {
      const saved = editing
        ? await updateConnection(editing.id, {
            maxOpenConnections: values.maxOpenConnections,
            maxIdleConnections: values.maxIdleConnections,
            maxLifetimeSeconds: values.maxLifetimeSeconds,
            maxIdleTimeSeconds: values.maxIdleTimeSeconds,
          })
        : await createConnection(values);
      setConnections((current) =>
        editing
          ? current.map((item) => (item.id === saved.id ? saved : item))
          : [...current, saved],
      );
      setFormOpen(false);
      setEditing(undefined);
      toast.success(editing ? "Connection updated." : "Connection created.");
    } catch (error) {
      setActionError(errorMessage(error, "Could not save the connection."));
    } finally {
      setBusy(false);
    }
  }

  async function changeConnectionState(
    connection: ConnectionSummary,
    connected: boolean,
  ) {
    setPendingConnectionId(connection.id);
    setActionError(undefined);
    try {
      await setConnectionConnected(connection.id, connected);
      await refresh();
      toast.success(`Connection ${connected ? "connected" : "disconnected"}.`);
    } catch (error) {
      setActionError(
        errorMessage(error, "Could not change the connection state."),
      );
    } finally {
      setPendingConnectionId(undefined);
    }
  }

  async function removeConnection() {
    if (!deleting) return;
    setBusy(true);
    setActionError(undefined);
    try {
      await deleteConnection(deleting.id);
      setConnections((items) =>
        items.filter((item) => item.id !== deleting.id),
      );
      setDeleting(undefined);
      toast.success("Connection deleted.");
    } catch (error) {
      setActionError(errorMessage(error, "Could not delete the connection."));
    } finally {
      setBusy(false);
    }
  }

  function editConnection(connection: ConnectionSummary) {
    setEditing(connection);
    setFormOpen(true);
  }

  const initialValue: ConnectionFormValues | undefined = editing
    ? {
        name: editing.name,
        engine: editing.engine,
        host: "",
        port: editing.engine === "postgres" ? 5432 : 3306,
        database: "",
        username: "",
        password: "",
        sslMode: "prefer",
        maxOpenConnections: editing.maxOpenConnections,
        maxIdleConnections: editing.maxIdleConnections,
        maxLifetimeSeconds: editing.maxLifetimeSeconds,
        maxIdleTimeSeconds: editing.maxIdleTimeSeconds,
        connectionTimeoutSeconds: 10,
      }
    : undefined;

  return (
    <div className="space-y-5">
      <PageHeader
        actions={
          <Button
            onClick={() => {
              setEditing(undefined);
              setFormOpen((open) => !open);
            }}
          >
            {formOpen ? "Close form" : "Add connection"}
          </Button>
        }
        description="Configure and monitor server-managed target databases."
        title="Connections"
      />
      {actionError ? (
        <ErrorState message={actionError} title="Connection action failed" />
      ) : null}
      {loadError ? (
        <ErrorState message={loadError} onRetry={() => void refresh()} />
      ) : null}
      {formOpen ? (
        <Card>
          <CardHeader>
            <CardTitle>
              {editing ? `Edit ${editing.name}` : "New connection"}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {editing ? (
              <p className="mb-4 text-sm text-muted-foreground">
                The connection target and credentials cannot be changed here.
                Only connection-pool settings can be updated.
              </p>
            ) : (
              <p className="mb-4 text-sm text-muted-foreground">
                Saving verifies the new database connection. Credentials are
                never returned by the backend.
              </p>
            )}
            <ConnectionForm
              busy={busy}
              editing={Boolean(editing)}
              initialValue={initialValue}
              onSubmit={saveConnection}
              key={editing?.id ?? "new"}
            />
          </CardContent>
        </Card>
      ) : null}
      {loading ? (
        <LoadingState label="Loading database connections" />
      ) : loadError ? null : (
        <ConnectionList
          connections={connections}
          onConnect={(item) => void changeConnectionState(item, true)}
          onDelete={setDeleting}
          onDisconnect={(item) => void changeConnectionState(item, false)}
          onEdit={editConnection}
          pendingId={pendingConnectionId}
        />
      )}
      <ConfirmDialog
        description={`Delete ${deleting?.name ?? "this connection"}? Groups with grants referencing this connection may also be affected.`}
        onConfirm={() => void removeConnection()}
        onOpenChange={(open) => {
          if (!open && !busy) setDeleting(undefined);
        }}
        open={Boolean(deleting)}
        title="Delete connection"
        confirmLabel="Delete connection"
        destructive
        pending={busy}
      />
    </div>
  );
}

export function AccessManagement() {
  const [groups, setGroups] = useState<Array<{ id: string; name: string }>>([]);
  const [connections, setConnections] = useState<
    Array<{ id: string; name: string; driver: string }>
  >([]);
  const [grants, setGrants] = useState<AccessGrant[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const [pendingGrant, setPendingGrant] = useState<string>();

  const loadAccess = useCallback(async () => {
    const data = await listGroupAccess();
    return {
      groups: data.groups.map(({ id, name }) => ({ id, name })),
      connections: data.connections.map(({ id, name, driver }) => ({
        id,
        name,
        driver,
      })),
      grants: data.groups.flatMap((group) =>
        group.grants.map((grant) => ({
          id: `${grant.groupId}:${grant.databaseId}`,
          groupId: grant.groupId,
          connectionId: grant.databaseId,
          level: accessGrantLevel(grant.access),
        })),
      ),
    };
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError(undefined);
    try {
      const data = await loadAccess();
      setGroups(data.groups);
      setConnections(data.connections);
      setGrants(data.grants);
    } catch (error) {
      setLoadError(errorMessage(error, "Could not load access grants."));
    } finally {
      setLoading(false);
    }
  }, [loadAccess]);

  useEffect(() => {
    let cancelled = false;
    loadAccess()
      .then((data) => {
        if (!cancelled) {
          setGroups(data.groups);
          setConnections(data.connections);
          setGrants(data.grants);
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setLoadError(errorMessage(error, "Could not load access grants."));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [loadAccess]);

  async function changeGrant(
    groupId: string,
    connectionId: string,
    level: AccessGrant["level"],
  ) {
    const key = `${groupId}:${connectionId}`;
    setPendingGrant(key);
    setActionError(undefined);
    try {
      const access =
        level === "view" ? "read" : level === "view_edit" ? "write" : null;
      await setGroupConnectionAccess(groupId, connectionId, access);
      setGrants((current) => {
        const remaining = current.filter((grant) => grant.id !== key);
        return level
          ? [...remaining, { id: key, groupId, connectionId, level }]
          : remaining;
      });
      toast.success("Access grant updated.");
    } catch (error) {
      setActionError(errorMessage(error, "Could not update the access grant."));
    } finally {
      setPendingGrant(undefined);
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        description="Choose a group, then search and manage access to its database connections."
        title="Group access"
      />
      {actionError ? (
        <ErrorState message={actionError} title="Could not update access" />
      ) : null}
      {loadError ? (
        <ErrorState message={loadError} onRetry={() => void refresh()} />
      ) : null}
      {loading ? (
        <LoadingState label="Loading group access" />
      ) : loadError ? null : (
        <GroupAccessEditor
          connections={connections}
          disabled={Boolean(pendingGrant)}
          grants={grants}
          groups={groups}
          onChange={changeGrant}
        />
      )}
    </div>
  );
}

export function DatabaseDirectoryClient() {
  const [databases, setDatabases] = useState<DatabaseSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      setDatabases(await listDatabaseConnections());
    } catch (loadError) {
      setError(errorMessage(loadError, "Could not load databases."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    listDatabaseConnections(controller.signal)
      .then((items) => {
        if (!controller.signal.aborted) setDatabases(items);
      })
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setError(errorMessage(loadError, "Could not load databases."));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, []);

  if (loading) return <LoadingState label="Loading available databases" />;
  if (error) {
    return (
      <ErrorState
        message={error}
        onRetry={() => void refresh()}
        title="Could not load databases"
      />
    );
  }
  return <DatabaseList databases={databases} />;
}

export function DatabaseWorkspace({
  id,
  activeTab,
}: {
  id: string;
  activeTab: WorkspaceTab;
}) {
  const [loadedWorkspace, setLoadedWorkspace] = useState<{
    key: string;
    database: DatabaseSummary;
    schemas: SchemaNode[];
  }>();
  const [loadError, setLoadError] = useState<{ key: string; message: string }>();
  const [reload, setReload] = useState(0);
  const loadKey = `${id}:${reload}`;

  useEffect(() => {
    const controller = new AbortController();
    Promise.all([
      getDatabaseConnection(id, controller.signal),
      loadDatabaseSchemas(id, controller.signal),
    ])
      .then(([connection, loadedSchemas]) => {
        setLoadedWorkspace({
          key: loadKey,
          database: connection,
          schemas: loadedSchemas,
        });
      })
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setLoadError({
            key: loadKey,
            message: errorMessage(loadError, "Could not load this database."),
          });
        }
      });
    return () => controller.abort();
  }, [id, loadKey]);

  const currentWorkspace =
    loadedWorkspace?.key === loadKey ? loadedWorkspace : undefined;
  const currentError = loadError?.key === loadKey ? loadError.message : undefined;
  if (!currentWorkspace && !currentError) {
    return <LoadingState label="Loading database workspace" rows={5} />;
  }
  if (currentError || !currentWorkspace) {
    return (
      <ErrorState
        message={currentError ?? "This database is unavailable."}
        onRetry={() => setReload((current) => current + 1)}
        title="Could not load database"
      />
    );
  }

  return (
    <DatabaseWorkspaceShell
      activeTab={activeTab}
      database={currentWorkspace.database}
    >
      {activeTab === "schema" ? (
        <SchemaWorkspace databaseId={id} schemas={currentWorkspace.schemas} />
      ) : (
        <QueryWorkspace databaseId={id} />
      )}
    </DatabaseWorkspaceShell>
  );
}

type TableSelection = { schema: string; table: string };
type ViewerRow = { key: string; values: unknown[] };
const tablePageSize = 50;
type TableDetailsPayload = {
  rowsPage?: DatabaseRowsPage;
  columns?: DatabaseColumn[];
  constraints?: DatabaseConstraint[];
  indexes?: DatabaseIndex[];
};

function SchemaWorkspace({
  databaseId,
  schemas,
}: {
  databaseId: string;
  schemas: SchemaNode[];
}) {
  const [selection, setSelection] = useState<TableSelection>();
  const [tab, setTab] = useState<TableDetailTab>("data");
  const [page, setPage] = useState(1);
  const [sort, setSort] = useState<{ column: string; direction: "asc" | "desc" }>();
  const [loadedDetails, setLoadedDetails] = useState<{
    key: string;
    data: TableDetailsPayload;
  }>();
  const [loadError, setLoadError] = useState<{ key: string; message: string }>();
  const [requestVersion, setRequestVersion] = useState(0);
  const detailsKey = JSON.stringify([
    databaseId,
    selection?.schema,
    selection?.table,
    tab,
    page,
    sort?.column,
    sort?.direction,
    requestVersion,
  ]);

  useEffect(() => {
    if (!selection) return;
    const controller = new AbortController();
    const request =
      tab === "data"
        ? loadDatabaseRows(
            databaseId,
            selection.schema,
            selection.table,
            {
              limit: tablePageSize + 1,
              offset: (page - 1) * tablePageSize,
              sort: sort?.column,
              direction: sort?.direction,
            },
            controller.signal,
          ).then((rowsPage) => ({ rowsPage }))
        : tab === "columns"
          ? loadDatabaseColumns(
              databaseId,
              selection.schema,
              selection.table,
              controller.signal,
            ).then((columns) => ({ columns }))
          : tab === "constraints"
            ? loadDatabaseConstraints(
                databaseId,
                selection.schema,
                selection.table,
                controller.signal,
              ).then((constraints) => ({ constraints }))
            : loadDatabaseIndexes(
                databaseId,
                selection.schema,
                selection.table,
                controller.signal,
              ).then((indexes) => ({ indexes }));

    request
      .then((data) => {
        setLoadedDetails({ key: detailsKey, data });
      })
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setLoadError({
            key: detailsKey,
            message: errorMessage(loadError, "Could not load table details."),
          });
        }
      });
    return () => controller.abort();
  }, [databaseId, detailsKey, page, requestVersion, selection, sort, tab]);

  const currentDetails =
    loadedDetails?.key === detailsKey ? loadedDetails.data : undefined;
  const currentError =
    loadError?.key === detailsKey ? loadError.message : undefined;
  const loading = Boolean(selection && !currentDetails && !currentError);
  const rowsPage = currentDetails?.rowsPage;
  const columnsInfo = currentDetails?.columns ?? [];
  const constraints = currentDetails?.constraints ?? [];
  const indexes = currentDetails?.indexes ?? [];

  const rows = useMemo(
    () =>
      (rowsPage?.rows ?? [])
        .slice(0, tablePageSize)
        .map((values, index) => ({
          key: String((rowsPage?.offset ?? 0) + index),
          values,
        })),
    [rowsPage],
  );
  const dataColumns = useMemo(
    () =>
      (rowsPage?.columns ?? []).map((name, index) => ({
        id: name,
        header: name,
        cell: (row: ViewerRow) => formatDatabaseValue(row.values[index]),
      })),
    [rowsPage?.columns],
  );
  const hasNext = (rowsPage?.rows.length ?? 0) > tablePageSize;

  function selectTable(schema: string, table: string) {
    setSelection({ schema, table });
    setTab("data");
    setPage(1);
    setSort(undefined);
  }

  function changeSort(column: string) {
    setSort((current) => ({
      column,
      direction:
        current?.column === column && current.direction === "asc"
          ? "desc"
          : "asc",
    }));
    setPage(1);
  }

  return (
    <div className="grid gap-5 lg:grid-cols-[260px_minmax(0,1fr)]">
      <SchemaTree
        onSelect={selectTable}
        schemas={schemas}
        selectedSchema={selection?.schema}
        selectedTable={selection?.table}
      />
      {selection ? (
        <section className="min-w-0 space-y-4">
          <div>
            <h2 className="text-lg font-semibold">{selection.table}</h2>
            <p className="text-sm text-muted-foreground">
              Schema: {selection.schema}
            </p>
          </div>
          <TableDetailsTabs
            activeTab={tab}
            onTabChange={(nextTab) => {
              setTab(nextTab);
              setPage(1);
            }}
          >
            {currentError ? (
              <ErrorState
                message={currentError}
                onRetry={() => setRequestVersion((current) => current + 1)}
                title="Could not load table details"
              />
            ) : loading ? (
              <LoadingState label={`Loading table ${tab}`} rows={3} />
            ) : tab === "data" ? (
              <TableDataGrid
                columns={dataColumns}
                getRowId={(row) => row.key}
                hasNext={hasNext}
                onPageChange={setPage}
                onSort={changeSort}
                page={page}
                pageSize={tablePageSize}
                rows={rows}
                total={(rowsPage?.offset ?? 0) + rows.length}
              />
            ) : (
              <TableMetadata
                columns={columnsInfo}
                constraints={constraints}
                indexes={indexes}
                tab={tab}
              />
            )}
          </TableDetailsTabs>
        </section>
      ) : (
        <EmptyState
          description="Choose a table from the schema tree to inspect its data and structure."
          title="Select a table"
        />
      )}
    </div>
  );
}

function TableMetadata({
  tab,
  columns,
  constraints,
  indexes,
}: {
  tab: TableDetailTab;
  columns: DatabaseColumn[];
  constraints: DatabaseConstraint[];
  indexes: DatabaseIndex[];
}) {
  const items =
    tab === "columns"
      ? columns.map(
          (column) =>
            `${column.name} · ${column.type} · ${column.nullable ? "nullable" : "not null"}${column.default === undefined ? "" : ` · default ${formatDatabaseValue(column.default)}`}`,
        )
      : tab === "constraints"
        ? constraints.map(
            (constraint) =>
              `${constraint.name} · ${constraint.definition || `${constraint.type} (${constraint.columns.join(", ")})`}`,
          )
        : indexes.map(
            (index) =>
              `${index.name} · ${index.unique ? "unique " : ""}${index.method || "index"} (${index.columns.join(", ")})`,
          );
  if (items.length === 0) {
    return <EmptyState title={`No ${tab} found`} />;
  }
  return (
    <ul className="divide-y rounded-lg border bg-background">
      {items.map((item) => (
        <li className="p-3 text-sm" key={item}>
          {item}
        </li>
      ))}
    </ul>
  );
}

function formatDatabaseValue(value: unknown): string {
  if (value === null) return "NULL";
  if (typeof value === "string") return value;
  if (typeof value === "object") return JSON.stringify(value) ?? String(value);
  return String(value);
}

function QueryWorkspace({ databaseId }: { databaseId: string }) {
  const [sql, setSql] = useState("SELECT 1;");
  const [result, setResult] = useState<DatabaseQueryResult>();
  const [elapsedMs, setElapsedMs] = useState<number>();
  const [error, setError] = useState<string>();
  const [running, setRunning] = useState(false);
  const queryController = useRef<AbortController | undefined>(undefined);

  useEffect(
    () => () => queryController.current?.abort(),
    [],
  );

  const columns = useMemo(
    () =>
      (result?.columns ?? []).map((name, index) => ({
        id: `${index}-${name}`,
        header: name,
        cell: (row: ViewerRow) => formatDatabaseValue(row.values[index]),
      })),
    [result?.columns],
  );
  const rows = useMemo(
    () =>
      (result?.rows ?? []).map((values, index) => ({
        key: String(index),
        values,
      })),
    [result?.rows],
  );

  async function runQuery() {
    const controller = new AbortController();
    queryController.current = controller;
    setRunning(true);
    setError(undefined);
    setResult(undefined);
    setElapsedMs(undefined);
    const started = performance.now();
    try {
      const queryResult = await executeDatabaseQuery(
        databaseId,
        sql,
        controller.signal,
      );
      setResult(queryResult);
      setElapsedMs(Math.round(performance.now() - started));
    } catch (queryError) {
      if (!controller.signal.aborted) {
        setError(errorMessage(queryError, "Could not run the query."));
      }
    } finally {
      if (queryController.current === controller) {
        queryController.current = undefined;
        setRunning(false);
      }
    }
  }

  function cancelQuery() {
    queryController.current?.abort();
    queryController.current = undefined;
    setRunning(false);
    toast.info("Query cancelled.");
  }

  return (
    <div className="space-y-6">
      <SqlEditor
        onCancel={cancelQuery}
        onChange={setSql}
        onRun={() => void runQuery()}
        running={running}
        value={sql}
      />
      {error ? (
        <ErrorState message={error} title="Query failed" />
      ) : result ? (
        <QueryResults
          columns={columns}
          getRowId={(row) => row.key}
          rows={rows}
          elapsedMs={elapsedMs}
          truncated={result.rows.length === 500}
        />
      ) : null}
    </div>
  );
}
