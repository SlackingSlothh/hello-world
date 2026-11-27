"use client";

import Link from "next/link";
import {
  useCallback,
  useEffect,
  useMemo,
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
import type { MockQueryRow, MockTableRow } from "@/lib/mock-data";

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

export function DatabaseWorkspace({
  database,
  schemas,
  activeTab,
  tableRows,
  queryRows,
}: {
  database: DatabaseSummary;
  schemas: SchemaNode[];
  activeTab: WorkspaceTab;
  tableRows: MockTableRow[];
  queryRows: MockQueryRow[];
}) {
  return (
    <DatabaseWorkspaceShell activeTab={activeTab} database={database}>
      {activeTab === "schema" ? (
        <SchemaWorkspace rows={tableRows} schemas={schemas} />
      ) : null}
      {activeTab === "query" ? <QueryWorkspace rows={queryRows} /> : null}
    </DatabaseWorkspaceShell>
  );
}

function SchemaWorkspace({
  schemas,
  rows,
}: {
  schemas: SchemaNode[];
  rows: MockTableRow[];
}) {
  const [selection, setSelection] = useState<{
    schema: string;
    table: string;
  }>();
  const [tab, setTab] = useState<TableDetailTab>("data");
  const columns = useMemo(
    () => [
      { id: "id", header: "ID", cell: (row: { id: string }) => row.id },
      { id: "name", header: "Name", cell: (row: { name: string }) => row.name },
      {
        id: "email",
        header: "Email",
        cell: (row: { email: string }) => row.email,
      },
      {
        id: "status",
        header: "Status",
        cell: (row: { status: string }) => row.status,
      },
    ],
    [],
  );
  return (
    <div className="grid gap-5 lg:grid-cols-[260px_minmax(0,1fr)]">
      <SchemaTree
        onSelect={(schema, table) => setSelection({ schema, table })}
        schemas={schemas}
        selectedSchema={selection?.schema}
        selectedTable={selection?.table}
        singleLevel={schemas.length === 1 && schemas[0]?.name === "sample"}
      />
      {selection ? (
        <section className="min-w-0 space-y-4">
          <div>
            <h2 className="text-lg font-semibold">{selection.table}</h2>
            <p className="text-sm text-muted-foreground">
              Schema: {selection.schema}
            </p>
          </div>
          <TableDetailsTabs activeTab={tab} onTabChange={setTab}>
            {tab === "data" ? (
              <TableDataGrid
                columns={columns}
                getRowId={(row) => row.id}
                onPageChange={() => undefined}
                onSort={() => toast.info("Mock sorting selected.")}
                page={1}
                pageSize={50}
                rows={rows}
                total={rows.length}
              />
            ) : (
              <DataDetails tab={tab} />
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

function DataDetails({ tab }: { tab: TableDetailTab }) {
  const details = {
    columns: [
      "id · uuid · primary key",
      "name · text · not null",
      "email · text · unique",
    ],
    constraints: [
      "customers_pkey · PRIMARY KEY (id)",
      "customers_email_key · UNIQUE (email)",
    ],
    indices: [
      "customers_pkey · btree (id)",
      "customers_email_key · btree (email)",
    ],
    data: [],
  }[tab];
  return (
    <ul className="divide-y rounded-lg border bg-background">
      {details.map((item) => (
        <li className="p-3 text-sm" key={item}>
          {item}
        </li>
      ))}
    </ul>
  );
}

function QueryWorkspace({ rows }: { rows: MockQueryRow[] }) {
  const [sql, setSql] = useState(
    "SELECT id, name, email, status FROM customers LIMIT 100;",
  );
  const [hasRun, setHasRun] = useState(false);
  const [running, setRunning] = useState(false);
  const columns = useMemo(
    () => [
      { id: "id", header: "ID", cell: (row: { id: number }) => row.id },
      { id: "name", header: "Name", cell: (row: { name: string }) => row.name },
      {
        id: "status",
        header: "Status",
        cell: (row: { status: string }) => row.status,
      },
    ],
    [],
  );
  async function runQuery() {
    setRunning(true);
    await new Promise((resolve) => setTimeout(resolve, 400));
    setRunning(false);
    setHasRun(true);
    toast.success("Mock read-only query completed.");
  }

  return (
    <div className="space-y-6">
      <SqlEditor
        onCancel={() => {
          setRunning(false);
          toast.info("Mock query cancelled.");
        }}
        onChange={setSql}
        onRun={() => void runQuery()}
        running={running}
        value={sql}
      />
      {hasRun ? (
        <QueryResults
          columns={columns}
          getRowId={(row) => String(row.id)}
          rows={rows}
          elapsedMs={18}
          truncated={false}
        />
      ) : null}
    </div>
  );
}
