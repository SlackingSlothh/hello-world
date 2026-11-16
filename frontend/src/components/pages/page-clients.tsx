"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  AccessGrantMatrix,
  AdminModeSwitcher,
  ConnectionForm,
  ConnectionList,
  GroupEditor,
  GroupList,
  type AccessGrant,
  type ConnectionFormValues,
  type ConnectionSummary,
  type GroupSummary,
  type MemberOption,
} from "@/components/admin/admin-panels";
import { AuthForm } from "@/components/auth/auth-form";
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
import { EmptyState } from "@/components/shared/async-states";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import { PageHeader } from "@/components/shared/page-header";
import type { MockQueryRow, MockTableRow } from "@/lib/mock-data";

export function AuthPageForm({
  mode,
  nextPath,
  sessionExpired = false,
}: {
  mode: "login" | "register";
  nextPath?: string;
  sessionExpired?: boolean;
}) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    await new Promise((resolve) => setTimeout(resolve, 300));
    toast.success(
      mode === "login"
        ? "Demo sign-in succeeded."
        : "Demo account created. Ask an administrator to grant access.",
    );
    setBusy(false);
    router.push(mode === "register" ? "/login" : safeReturnPath(nextPath));
  }

  return (
    <AuthForm
      error={undefined}
      mode={mode}
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

export function AdminHome({ isAdmin }: { isAdmin: boolean }) {
  const router = useRouter();
  const [mode, setMode] = useState<"user" | "admin">("admin");
  return (
    <div>
      <PageHeader
        actions={
          isAdmin ? (
            <AdminModeSwitcher
              mode={mode}
              onSwitch={(nextMode) => {
                setMode(nextMode);
                router.push(nextMode === "admin" ? "/admin" : "/db");
              }}
            />
          ) : undefined
        }
        description="Manage database connections, groups, and access grants."
        title="Admin"
      />
      <ul className="grid gap-4 md:grid-cols-3">
        {[
          ["Connections", "Configure managed database connections.", "/admin/connections"],
          ["Groups", "Manage group members and leaders.", "/admin/groups"],
          ["Access", "Grant groups view or edit access.", "/admin/access"],
        ].map(([title, description, href]) => (
          <li key={href}>
            <Link className="block h-full rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" href={href}>
              <Card className="h-full transition-colors hover:bg-muted/40">
                <CardHeader>
                  <CardTitle>{title}</CardTitle>
                  <CardDescription>{description}</CardDescription>
                </CardHeader>
                <CardContent className="text-sm font-medium text-primary">Open {title.toLowerCase()} →</CardContent>
              </Card>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ConnectionManagement({
  initialConnections,
}: {
  initialConnections: ConnectionSummary[];
}) {
  const [connections, setConnections] = useState(initialConnections);
  const [formOpen, setFormOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<ConnectionSummary>();
  const [deleting, setDeleting] = useState<ConnectionSummary>();

  async function testConnection(values: ConnectionFormValues) {
    const success = Boolean(values.host.trim() && values.database.trim());
    if (success) toast.success("Mock connection test succeeded.");
    else toast.error("Host and database are required.");
  }

  async function saveConnection(values: ConnectionFormValues) {
    setBusy(true);
    setConnections((current) => {
      const record: ConnectionSummary = {
        id: editing?.id ?? `connection-${Date.now()}`,
        name: values.name,
        engine: values.engine,
        host: values.host,
        port: values.port,
        database: values.database,
        status: editing?.status ?? "disconnected",
        activeConnections: editing?.activeConnections ?? 0,
        idleConnections: editing?.idleConnections ?? 0,
      };
      return editing
        ? current.map((item) => item.id === editing.id ? record : item)
        : [...current, record];
    });
    setBusy(false);
    setFormOpen(false);
    setEditing(undefined);
    toast.success(editing ? "Mock connection updated." : "Mock connection saved.");
  }

  function updateStatus(connection: ConnectionSummary, status: string) {
    setConnections((items) =>
      items.map((item) => (item.id === connection.id ? { ...item, status } : item)),
    );
    toast.success(`Mock connection ${status}.`);
  }

  function removeConnection(connection: ConnectionSummary) {
    setConnections((items) => items.filter((item) => item.id !== connection.id));
    setDeleting(undefined);
    toast.success("Mock connection removed.");
  }

  function editConnection(connection: ConnectionSummary) {
    setEditing(connection);
    setFormOpen(true);
  }

  const initialValue: ConnectionFormValues | undefined = editing
    ? {
        name: editing.name,
        engine: editing.engine,
        host: editing.host,
        port: editing.port,
        database: editing.database,
        username: "",
        password: "",
        sslMode: "prefer",
        maxOpenConnections: 10,
        maxIdleConnections: 5,
        connectionTimeoutSeconds: 10,
        statementTimeoutSeconds: 30,
      }
    : undefined;

  return (
    <div className="space-y-5">
      <PageHeader
        actions={
          <Button onClick={() => {
            setEditing(undefined);
            setFormOpen((open) => !open);
          }}>
            {formOpen ? "Close form" : "Add connection"}
          </Button>
        }
        description="Configure and monitor server-managed target databases."
        title="Connections"
      />
      {formOpen ? (
        <Card>
          <CardHeader>
            <CardTitle>{editing ? `Edit ${editing.name}` : "New connection"}</CardTitle>
          </CardHeader>
          <CardContent>
            <ConnectionForm
              busy={busy}
              editing={Boolean(editing)}
              initialValue={initialValue}
              onSubmit={saveConnection}
              onTest={testConnection}
              key={editing?.id ?? "new"}
            />
          </CardContent>
        </Card>
      ) : null}
      <ConnectionList
        connections={connections}
        onConnect={(item) => updateStatus(item, "connected")}
        onDelete={setDeleting}
        onDisconnect={(item) => updateStatus(item, "disconnected")}
        onEdit={editConnection}
      />
      <ConfirmDialog
        description={`Delete ${deleting?.name ?? "this connection"}? This mock action only changes the local demo view.`}
        onConfirm={() => deleting && removeConnection(deleting)}
        onOpenChange={(open) => {
          if (!open) setDeleting(undefined);
        }}
        open={Boolean(deleting)}
        title="Delete connection"
        confirmLabel="Delete connection"
        destructive
      />
    </div>
  );
}

export function GroupManagement({
  initialGroups,
  members,
}: {
  initialGroups: GroupSummary[];
  members: MemberOption[];
}) {
  const [groups, setGroups] = useState(initialGroups);
  const [editorOpen, setEditorOpen] = useState(false);
  const [name, setName] = useState("");
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [leaderId, setLeaderId] = useState("");
  const [editingGroup, setEditingGroup] = useState<GroupSummary>();

  function saveGroup() {
    const leader = members.find((member) => member.id === leaderId);
    if (!leader || !name.trim()) return;
    setGroups((current) => {
      const saved: GroupSummary = {
        id: editingGroup?.id ?? `group-${Date.now()}`,
        name: name.trim(),
        leaderName: leader.label,
        memberCount: memberIds.length,
        memberIds,
      };
      return editingGroup
        ? current.map((group) => group.id === editingGroup.id ? saved : group)
        : [...current, saved];
    });
    setName("");
    setMemberIds([]);
    setLeaderId("");
    setEditorOpen(false);
    setEditingGroup(undefined);
    toast.success(editingGroup ? "Mock group updated." : "Mock group created.");
  }

  function openEditor(group?: GroupSummary) {
    setEditingGroup(group);
    setName(group?.name ?? "");
    setMemberIds(group?.memberIds ?? []);
    setLeaderId(members.find((member) => member.label === group?.leaderName)?.id ?? "");
    setEditorOpen(true);
  }

  return (
    <div className="space-y-5">
      <PageHeader
        actions={
          <Button onClick={() => editorOpen ? setEditorOpen(false) : openEditor()}>
            {editorOpen ? "Close form" : "Create group"}
          </Button>
        }
        description="Every group must have exactly one leader who is also a member."
        title="Groups"
      />
      {editorOpen ? (
        <Card>
          <CardHeader>
            <CardTitle>{editingGroup ? `Edit ${editingGroup.name}` : "New group"}</CardTitle>
            <CardDescription>The Administrators system group cannot be deleted.</CardDescription>
          </CardHeader>
          <CardContent>
            <GroupEditor
              leaderId={leaderId}
              members={members}
              name={name}
              onLeaderChange={setLeaderId}
              onMembersChange={(ids) => {
                setMemberIds(ids);
                if (!ids.includes(leaderId)) setLeaderId("");
              }}
              onNameChange={setName}
              onSubmit={saveGroup}
              selectedMemberIds={memberIds}
            />
          </CardContent>
        </Card>
      ) : null}
      <GroupList
        groups={groups}
        onDelete={(group) => {
          setGroups((current) => current.filter((item) => item.id !== group.id));
          toast.success("Mock group removed.");
        }}
        onEdit={openEditor}
      />
    </div>
  );
}

export function AccessManagement({
  groups,
  connections,
  initialGrants,
}: {
  groups: Array<{ id: string; name: string }>;
  connections: Array<{ id: string; name: string }>;
  initialGrants: AccessGrant[];
}) {
  const [grants, setGrants] = useState(initialGrants);
  function changeGrant(groupId: string, connectionId: string, level: AccessGrant["level"]) {
    setGrants((current) => {
      const existing = current.find(
        (grant) => grant.groupId === groupId && grant.connectionId === connectionId,
      );
      if (!level) return current.filter((grant) => grant.id !== existing?.id);
      if (existing) {
        return current.map((grant) =>
          grant.id === existing.id ? { ...grant, level } : grant,
        );
      }
      return [
        ...current,
        { id: `grant-${Date.now()}`, groupId, connectionId, level },
      ];
    });
    toast.success("Mock access grant updated.");
  }

  return (
    <div className="space-y-5">
      <PageHeader
        description="Access changes affect active sessions and pending migration approvals."
        title="Group access"
      />
      <AccessGrantMatrix
        connections={connections}
        grants={grants}
        groups={groups}
        onChange={changeGrant}
      />
    </div>
  );
}

export function DatabaseWorkspace({
  database,
  schemas,
  activeTab,
  history,
  drafts,
  requests,
  approvals,
  initialJob,
  tableRows,
  queryRows,
  preview,
}: {
  database: DatabaseSummary;
  schemas: SchemaNode[];
  activeTab: WorkspaceTab;
  history: MigrationRecord[];
  drafts: DraftSummary[];
  requests: RequestSummary[];
  approvals: ApprovalProgress[];
  initialJob: JobSummary;
  tableRows: MockTableRow[];
  queryRows: MockQueryRow[];
  preview: string;
}) {
  return (
    <DatabaseWorkspaceShell activeTab={activeTab} database={database}>
      {activeTab === "schema" ? <SchemaWorkspace rows={tableRows} schemas={schemas} /> : null}
      {activeTab === "query" ? <QueryWorkspace rows={queryRows} /> : null}
      {activeTab === "migration" ? (
        database.engine === "postgres" ? (
          <MigrationWorkspace
            approvals={approvals}
            drafts={drafts}
            history={history}
            initialJob={initialJob}
            preview={preview}
            requests={requests}
          />
        ) : (
          <EmptyState title="Migrations are unavailable for this database engine." />
        )
      ) : null}
    </DatabaseWorkspaceShell>
  );
}

function SchemaWorkspace({ schemas, rows }: { schemas: SchemaNode[]; rows: MockTableRow[] }) {
  const [selection, setSelection] = useState<{ schema: string; table: string }>();
  const [tab, setTab] = useState<TableDetailTab>("data");
  const columns = useMemo(
    () => [
      { id: "id", header: "ID", cell: (row: { id: string }) => row.id },
      { id: "name", header: "Name", cell: (row: { name: string }) => row.name },
      { id: "email", header: "Email", cell: (row: { email: string }) => row.email },
      { id: "status", header: "Status", cell: (row: { status: string }) => row.status },
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
            <p className="text-sm text-muted-foreground">Schema: {selection.schema}</p>
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
    columns: ["id · uuid · primary key", "name · text · not null", "email · text · unique"],
    constraints: ["customers_pkey · PRIMARY KEY (id)", "customers_email_key · UNIQUE (email)"],
    indices: ["customers_pkey · btree (id)", "customers_email_key · btree (email)"],
    data: [],
  }[tab];
  return (
    <ul className="divide-y rounded-lg border bg-background">
      {details.map((item) => (
        <li className="p-3 text-sm" key={item}>{item}</li>
      ))}
    </ul>
  );
}

function QueryWorkspace({ rows }: { rows: MockQueryRow[] }) {
  const [sql, setSql] = useState("SELECT id, name, email, status FROM customers LIMIT 100;");
  const [hasRun, setHasRun] = useState(false);
  const [running, setRunning] = useState(false);
  const columns = useMemo(
    () => [
      { id: "id", header: "ID", cell: (row: { id: number }) => row.id },
      { id: "name", header: "Name", cell: (row: { name: string }) => row.name },
      { id: "status", header: "Status", cell: (row: { status: string }) => row.status },
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
}: {
  history: MigrationRecord[];
  drafts: DraftSummary[];
  requests: RequestSummary[];
  approvals: ApprovalProgress[];
  initialJob: JobSummary;
  preview: string;
}) {
  const [drafts, setDrafts] = useState(initialDrafts);
  const [hideRollbacks, setHideRollbacks] = useState(false);
  const pollCount = useRef(0);
  const fetchJob = useCallback(async (jobId: string) => {
    await new Promise((resolve) => setTimeout(resolve, 250));
    pollCount.current += 1;
    return {
      ...initialJob,
      id: jobId,
      status: pollCount.current > 1 ? "succeeded" : "running",
      batchesCommitted: pollCount.current > 1 ? 10 : 5,
      rowsCommitted: pollCount.current > 1 ? 10000 : 5000,
    };
  }, [initialJob]);

  return (
    <div className="space-y-8">
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
            <Button onClick={() => toast.info("Mock draft form is ready below.")}>
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
            setDrafts((current) => current.filter((item) => item.id !== draft.id));
            toast.success("Mock draft deleted.");
          }}
          onDuplicate={(draft) => {
            setDrafts((current) => [
              ...current,
              { ...draft, id: `draft-${Date.now()}`, name: `${draft.name} copy`, status: "invalid" },
            ]);
            toast.success("Mock draft duplicated.");
          }}
          onEdit={(draft) => toast.info(`Mock editing ${draft.name}.`)}
          onRebase={(draft) => {
            setDrafts((current) =>
              current.map((item) => item.id === draft.id ? { ...item, status: "valid" } : item),
            );
            toast.success("Mock draft rebased.");
          }}
          onRequest={(draft) => toast.success(`Mock request submitted for ${draft.name}.`)}
          onValidate={(draft) => toast.success(`${draft.name} passed mock validation.`)}
        />
      </section>
      <section className="space-y-4">
        <PageHeader description="Approval status for pending changes." title="Requests" />
        <RequestList
          onSelect={(request) => toast.info(`Reviewing ${request.id}.`)}
          requests={requests}
        />
        <RequestReview
          approvals={approvals}
          onApprove={() => toast.success("Mock approval recorded.")}
          onReject={(comment) => toast.success(`Mock rejection recorded: ${comment}`)}
          sqlPreview={preview}
        />
      </section>
      <section className="space-y-4">
        <PageHeader description="Progress for background migration jobs." title="Jobs" />
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
