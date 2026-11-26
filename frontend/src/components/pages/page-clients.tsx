"use client";

import Link from "next/link";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  AccessGrantMatrix,
  ConnectionForm,
  ConnectionList,
  GroupEditor,
  GroupList,
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
  DraftEditor,
  DraftList,
  JobList,
  JobProgress,
  MigrationHistory,
  RequestList,
  RequestReview,
  type ApprovalProgress,
  type DraftSummary,
  type JobSummary,
  type MigrationRecord,
  type RequestSummary,
} from "@/components/migration/migration-panels";
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
  createGroup,
  deleteConnection,
  listAdminConnections,
  listGroupAccess,
  listGroupMembers,
  setConnectionConnected,
  setGroupConnectionAccess,
  setGroupMember,
  updateConnection,
  type AdminGroupData,
} from "@/lib/api/admin";
import { login } from "@/lib/api/auth";
import { databaseRouteKey } from "@/lib/database-routing";
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

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

type ManagedGroup = AdminGroupData["groups"][number] & {
  memberCount: number;
};

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
          ["Groups", "Create groups and manage their members.", "/admin/groups"],
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
        ? await updateConnection(editing.id, values)
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
      toast.success(
        `Connection ${connected ? "connected" : "disconnected"}.`,
      );
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

export function GroupManagement() {
  const [groups, setGroups] = useState<ManagedGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>();
  const [createError, setCreateError] = useState<string>();
  const [memberError, setMemberError] = useState<string>();
  const [creating, setCreating] = useState(false);
  const [createBusy, setCreateBusy] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [activeGroup, setActiveGroup] = useState<ManagedGroup>();
  const [members, setMembers] = useState<
    Awaited<ReturnType<typeof listGroupMembers>>
  >([]);
  const [membersLoaded, setMembersLoaded] = useState(false);
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [savedMemberIds, setSavedMemberIds] = useState<string[]>([]);
  const [membersBusy, setMembersBusy] = useState(false);

  const loadGroups = useCallback(async () => {
    const data = await listGroupAccess();
    const memberLists = await Promise.all(
      data.groups.map((group) => listGroupMembers(group.id)),
    );
    return data.groups.map((group, index) => ({
      ...group,
      memberCount: memberLists[index].filter((member) => member.member).length,
    }));
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError(undefined);
    try {
      setGroups(await loadGroups());
    } catch (error) {
      setLoadError(errorMessage(error, "Could not load groups."));
    } finally {
      setLoading(false);
    }
  }, [loadGroups]);

  useEffect(() => {
    let cancelled = false;
    loadGroups()
      .then((items) => {
        if (!cancelled) setGroups(items);
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setLoadError(errorMessage(error, "Could not load groups."));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [loadGroups]);

  async function createNewGroup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setCreateBusy(true);
    setCreateError(undefined);
    try {
      await createGroup(groupName);
      setGroupName("");
      setCreating(false);
      await refresh();
      toast.success("Group created.");
    } catch (error) {
      setCreateError(errorMessage(error, "Could not create the group."));
    } finally {
      setCreateBusy(false);
    }
  }

  async function openMembers(
    group: ManagedGroup,
  ) {
    setActiveGroup(group);
    setMembersBusy(true);
    setMemberError(undefined);
    setMembersLoaded(false);
    setMembers([]);
    setMemberIds([]);
    setSavedMemberIds([]);
    try {
      const groupMembers = await listGroupMembers(group.id);
      const currentIds = groupMembers
        .filter((member) => member.member)
        .map((member) => member.id);
      setMembers(groupMembers);
      setSavedMemberIds(currentIds);
      setMemberIds(currentIds);
      setMembersLoaded(true);
    } catch (error) {
      setMemberError(errorMessage(error, "Could not load group members."));
    } finally {
      setMembersBusy(false);
    }
  }

  async function reloadActiveMembers() {
    if (!activeGroup) return;
    const updatedMembers = await listGroupMembers(activeGroup.id);
    const updatedIds = updatedMembers
      .filter((member) => member.member)
      .map((member) => member.id);
    setMembers(updatedMembers);
    setSavedMemberIds(updatedIds);
    setMemberIds(updatedIds);
    setMembersLoaded(true);
  }

  async function saveMembers() {
    if (!activeGroup) return;
    setMembersBusy(true);
    setMemberError(undefined);
    try {
      const toAdd = memberIds.filter((id) => !savedMemberIds.includes(id));
      const toRemove = savedMemberIds.filter((id) => !memberIds.includes(id));
      for (const id of toAdd) {
        await setGroupMember(activeGroup.id, id, true);
      }
      for (const id of toRemove) {
        await setGroupMember(activeGroup.id, id, false);
      }
      await reloadActiveMembers();
      await refresh();
      toast.success("Group members updated.");
    } catch (error) {
      let message = errorMessage(error, "Could not update group members.");
      try {
        await reloadActiveMembers();
        message = `${message} The current membership was reloaded.`;
      } catch (reloadError) {
        message = `${message} Membership refresh failed: ${errorMessage(reloadError, "unknown error")}`;
      }
      setMemberError(message);
    } finally {
      setMembersBusy(false);
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        actions={
          <Button
            onClick={() => {
              setCreating((current) => !current);
              setCreateError(undefined);
            }}
          >
            {creating ? "Close form" : "Create group"}
          </Button>
        }
        description="Create groups and manage their memberships. Group rename and deletion are not supported by the backend."
        title="Groups"
      />
      {loadError ? (
        <ErrorState message={loadError} onRetry={() => void refresh()} />
      ) : null}
      {createError ? (
        <ErrorState message={createError} title="Could not create group" />
      ) : null}
      {creating ? (
        <Card>
          <CardHeader>
            <CardTitle>New group</CardTitle>
            <CardDescription>
              Groups are created without members. Add members after creation.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="flex flex-wrap items-end gap-3"
              onSubmit={(event) => void createNewGroup(event)}
            >
              <label className="min-w-60 flex-1 space-y-2 text-sm font-medium">
                Group name
                <Input
                  maxLength={100}
                  onChange={(event) => setGroupName(event.target.value)}
                  required
                  value={groupName}
                />
              </label>
              <Button disabled={createBusy} type="submit">
                {createBusy ? "Creating…" : "Create group"}
              </Button>
            </form>
          </CardContent>
        </Card>
      ) : null}
      {loading ? (
        <LoadingState label="Loading groups" />
      ) : loadError ? null : (
        <GroupList
          groups={groups.map((group) => ({
            id: group.id,
            name: group.name,
            memberCount: group.memberCount,
            system: group.isAdmin,
          }))}
          onEdit={(group) => {
            const selected = groups.find((item) => item.id === group.id);
            if (selected) void openMembers(selected);
          }}
        />
      )}
      {activeGroup ? (
        <Card>
          <CardHeader className="flex-row items-start justify-between gap-3">
            <div>
              <CardTitle>Members: {activeGroup.name}</CardTitle>
              <CardDescription>
                Users are listed with their current account status. Inactive
                users cannot be added.
              </CardDescription>
            </div>
            <Button
              onClick={() => {
                setActiveGroup(undefined);
                setMemberError(undefined);
              }}
              size="sm"
              variant="outline"
            >
              Close
            </Button>
          </CardHeader>
          <CardContent className="space-y-4">
            {memberError ? (
              <ErrorState
                message={memberError}
                onRetry={() => void openMembers(activeGroup)}
                title="Group membership"
              />
            ) : null}
            {membersBusy && !membersLoaded ? (
              <LoadingState label="Loading group members" rows={2} />
            ) : membersLoaded ? (
              <GroupEditor
                busy={membersBusy}
                disabled={membersBusy}
                members={members}
                onMembersChange={setMemberIds}
                onSubmit={() => void saveMembers()}
                selectedMemberIds={memberIds}
              />
            ) : null}
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

export function AccessManagement() {
  const [groups, setGroups] = useState<Array<{ id: string; name: string }>>([]);
  const [connections, setConnections] = useState<
    Array<{ id: string; name: string }>
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
      connections: data.connections.map(({ id, name }) => ({ id, name })),
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
        description="Set each group's database access to none, read, or write."
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
        <AccessGrantMatrix
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

export function MigrationDatabaseList({
  databases,
}: {
  databases: DatabaseSummary[];
}) {
  if (databases.length === 0) {
    return (
      <EmptyState
        description="Migration access is available for PostgreSQL databases where your group grants view + edit access."
        title="No databases available for migration"
      />
    );
  }
  return (
    <ul className="grid gap-4 md:grid-cols-2">
      {databases.map((database) => (
        <li key={database.id}>
          <Card>
            <CardHeader className="flex-row items-start justify-between gap-3">
              <div>
                <CardTitle>{database.name}</CardTitle>
                <CardDescription className="mt-1">
                  PostgreSQL · {database.status}
                </CardDescription>
              </div>
              <span className="rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">
                View + Edit
              </span>
            </CardHeader>
            <CardContent className="flex items-center justify-between gap-3">
              <div className="flex flex-wrap gap-2 text-sm text-muted-foreground">
                {database.migrationRunning ? <span>Migration running</span> : null}
                {database.awaitingReview ? <span>Request awaiting review</span> : null}
              </div>
              <Link
                className="inline-flex h-8 shrink-0 items-center justify-center rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                href={`/migration/${databaseRouteKey(database.name)}`}
              >
                Open migrations
              </Link>
            </CardContent>
          </Card>
        </li>
      ))}
    </ul>
  );
}

export function MigrationDatabaseWorkspace({
  database,
  history,
  drafts,
  requests,
  approvals,
  initialJob,
  preview,
}: {
  database: DatabaseSummary;
  history: MigrationRecord[];
  drafts: DraftSummary[];
  requests: RequestSummary[];
  approvals: ApprovalProgress[];
  initialJob: JobSummary;
  preview: string;
}) {
  return (
    <div className="space-y-5">
      <PageHeader
        breadcrumbs={[
          { label: "Migrations", href: "/migration" },
          { label: database.name },
        ]}
        description="Manage migration history, drafts, reviews, and execution jobs."
        title={`Migrations · ${database.name}`}
      />
      <MigrationWorkspace
        approvals={approvals}
        databaseName={database.name}
        drafts={drafts}
        history={history}
        initialJob={initialJob}
        preview={preview}
        requests={requests}
      />
    </div>
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

function MigrationWorkspace({
  history,
  drafts: initialDrafts,
  requests,
  approvals,
  initialJob,
  preview,
  databaseName,
}: {
  history: MigrationRecord[];
  drafts: DraftSummary[];
  requests: RequestSummary[];
  approvals: ApprovalProgress[];
  initialJob: JobSummary;
  preview: string;
  databaseName?: string;
}) {
  const [drafts, setDrafts] = useState(initialDrafts);
  const [hideRollbacks, setHideRollbacks] = useState(false);
  const pollCount = useRef(0);
  const fetchJob = useCallback(
    async (jobId: string) => {
      await new Promise((resolve) => setTimeout(resolve, 250));
      pollCount.current += 1;
      return {
        ...initialJob,
        id: jobId,
        status: pollCount.current > 1 ? "succeeded" : "running",
        batchesCommitted: pollCount.current > 1 ? 10 : 5,
        rowsCommitted: pollCount.current > 1 ? 10000 : 5000,
      };
    },
    [initialJob],
  );

  return (
    <div className="space-y-8">
      {databaseName ? (
        <p className="text-sm text-muted-foreground">
          Managing migrations for <span className="font-medium text-foreground">{databaseName}</span>
        </p>
      ) : null}
      <section className="space-y-4">
        <PageHeader
          description="Applied changes and rollback availability."
          title="Migration history"
        />
        <MigrationHistory
          hideRollbacks={hideRollbacks}
          onHideRollbacksChange={setHideRollbacks}
          records={history}
        />
      </section>
      <section className="space-y-4">
        <PageHeader
          actions={
            <Button
              onClick={() => toast.info("Mock draft form is ready below.")}
            >
              New draft
            </Button>
          }
          description="Create, validate, and request migration drafts."
          title="Drafts"
        />
        <Card>
          <CardContent className="pt-6">
            <DraftEditor
              initialValue={{
                name: "",
                description: "",
                upSql: "",
                downSql: "",
                irreversible: false,
                mode: "single",
                batchSize: 1000,
                cursorColumn: "",
              }}
              onSave={(value) => {
                setDrafts((current) => [
                  ...current,
                  {
                    id: `draft-${Date.now()}`,
                    name: value.name,
                    status: "valid",
                    mode: value.mode,
                  },
                ]);
                toast.success("Mock draft saved.");
              }}
            />
          </CardContent>
        </Card>
        <DraftList
          canEdit
          drafts={drafts}
          onDelete={(draft) => {
            setDrafts((current) =>
              current.filter((item) => item.id !== draft.id),
            );
            toast.success("Mock draft deleted.");
          }}
          onDuplicate={(draft) => {
            setDrafts((current) => [
              ...current,
              {
                ...draft,
                id: `draft-${Date.now()}`,
                name: `${draft.name} copy`,
                status: "invalid",
              },
            ]);
            toast.success("Mock draft duplicated.");
          }}
          onEdit={(draft) => toast.info(`Mock editing ${draft.name}.`)}
          onRebase={(draft) => {
            setDrafts((current) =>
              current.map((item) =>
                item.id === draft.id ? { ...item, status: "valid" } : item,
              ),
            );
            toast.success("Mock draft rebased.");
          }}
          onRequest={(draft) =>
            toast.success(`Mock request submitted for ${draft.name}.`)
          }
          onValidate={(draft) =>
            toast.success(`${draft.name} passed mock validation.`)
          }
        />
      </section>
      <section className="space-y-4">
        <PageHeader
          description="Approval status for pending changes."
          title="Requests"
        />
        <RequestList
          onSelect={(request) => toast.info(`Reviewing ${request.id}.`)}
          requests={requests}
        />
        <RequestReview
          approvals={approvals}
          onApprove={() => toast.success("Mock approval recorded.")}
          onReject={(comment) =>
            toast.success(`Mock rejection recorded: ${comment}`)
          }
          sqlPreview={preview}
        />
      </section>
      <section className="space-y-4">
        <PageHeader
          description="Progress for background migration jobs."
          title="Jobs"
        />
        <JobList
          jobs={[initialJob]}
          onPageChange={() => undefined}
          onSelect={() => toast.info("Showing the current job progress.")}
          page={1}
          pageSize={20}
          total={1}
        />
        <JobProgress fetchJob={fetchJob} initialJob={initialJob} />
      </section>
    </div>
  );
}
