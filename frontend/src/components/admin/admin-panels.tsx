"use client";

import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  DataTable,
  type DataTableColumn,
} from "@/components/shared/data-table";
import { PaginationControls } from "@/components/shared/pagination-controls";
import { EmptyState } from "@/components/shared/async-states";
import { AccessBadge, StatusBadge } from "@/components/shared/status-badge";

export type ConnectionSummary = {
  id: string;
  name: string;
  engine: "postgres" | "mysql" | "mariadb";
  status: "connected" | "disconnected";
  activeConnections: number;
  idleConnections: number;
  maxOpenConnections: number;
  maxIdleConnections: number;
  maxLifetimeSeconds: number;
  maxIdleTimeSeconds: number;
};

export function ConnectionCard({
  connection,
  actions,
}: {
  connection: ConnectionSummary;
  actions?: ReactNode;
}) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between">
        <div>
          <CardTitle>{connection.name}</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            {connection.engine}
          </p>
        </div>
        <StatusBadge status={connection.status} />
      </CardHeader>
      <CardContent className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
        <span>
          Pool: {connection.activeConnections} active,{" "}
          {connection.idleConnections} idle · max{" "}
          {connection.maxOpenConnections || "unlimited"} open
        </span>
        <div className="flex flex-wrap gap-2">{actions}</div>
      </CardContent>
    </Card>
  );
}

export function ConnectionList({
  connections,
  onEdit,
  onConnect,
  onDisconnect,
  onDelete,
  pendingId,
}: {
  connections: ConnectionSummary[];
  onEdit?: (connection: ConnectionSummary) => void;
  onConnect?: (connection: ConnectionSummary) => void;
  onDisconnect?: (connection: ConnectionSummary) => void;
  onDelete?: (connection: ConnectionSummary) => void;
  pendingId?: string;
}) {
  const columns: DataTableColumn<ConnectionSummary>[] = [
    { id: "name", header: "Connection", cell: (item) => item.name },
    { id: "engine", header: "Engine", cell: (item) => item.engine },
    {
      id: "pool",
      header: "Pool",
      cell: (item) =>
        `${item.activeConnections} active · ${item.idleConnections} idle · max ${item.maxOpenConnections || "unlimited"}`,
    },
    {
      id: "status",
      header: "Status",
      cell: (item) => <StatusBadge status={item.status} />,
    },
    {
      id: "actions",
      header: "Actions",
      cell: (item) => (
        <div className="flex flex-wrap gap-1">
          {onEdit ? (
            <Button
              disabled={pendingId === item.id}
              onClick={() => onEdit(item)}
              size="sm"
              variant="outline"
            >
              Edit
            </Button>
          ) : null}
          {item.status === "connected" ? (
            onDisconnect ? (
              <Button
                disabled={pendingId === item.id}
                onClick={() => onDisconnect(item)}
                size="sm"
                variant="outline"
              >
                {pendingId === item.id ? "Working…" : "Disconnect"}
              </Button>
            ) : null
          ) : onConnect ? (
            <Button
              disabled={pendingId === item.id}
              onClick={() => onConnect(item)}
              size="sm"
              variant="outline"
            >
              {pendingId === item.id ? "Working…" : "Connect"}
            </Button>
          ) : null}
          {onDelete ? (
            <Button
              aria-label={`Delete ${item.name}`}
              disabled={pendingId === item.id}
              onClick={() => onDelete(item)}
              size="icon-sm"
              variant="destructive"
            >
              <Trash2 aria-hidden="true" />
            </Button>
          ) : null}
        </div>
      ),
    },
  ];

  return (
    <DataTable
      caption="Managed database connections"
      columns={columns}
      emptyDescription="Add a connection to make a database available to groups."
      emptyTitle="No connections configured"
      getRowId={(item) => item.id}
      rows={connections}
    />
  );
}

export type ConnectionFormValues = {
  name: string;
  engine: ConnectionSummary["engine"];
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  sslMode: string;
  maxOpenConnections: number;
  maxIdleConnections: number;
  maxLifetimeSeconds: number;
  maxIdleTimeSeconds: number;
  connectionTimeoutSeconds: number;
};

export type ConnectionPoolValues = Pick<
  ConnectionFormValues,
  | "maxOpenConnections"
  | "maxIdleConnections"
  | "maxLifetimeSeconds"
  | "maxIdleTimeSeconds"
>;

const defaultConnection: ConnectionFormValues = {
  name: "",
  engine: "postgres",
  host: "",
  port: 5432,
  database: "",
  username: "",
  password: "",
  sslMode: "prefer",
  maxOpenConnections: 10,
  maxIdleConnections: 5,
  maxLifetimeSeconds: 60,
  maxIdleTimeSeconds: 5,
  connectionTimeoutSeconds: 10,
};

export function ConnectionForm({
  initialValue = defaultConnection,
  editing = false,
  busy = false,
  onTest,
  onSubmit,
}: {
  initialValue?: ConnectionFormValues;
  editing?: boolean;
  busy?: boolean;
  onTest?: (values: ConnectionFormValues) => void | Promise<void>;
  onSubmit: (values: ConnectionFormValues) => void | Promise<void>;
}) {
  const [values, setValues] = useState(initialValue);

  function update<K extends keyof ConnectionFormValues>(
    key: K,
    value: ConnectionFormValues[K],
  ) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function submit(
    event: FormEvent<HTMLFormElement>,
    callback: typeof onSubmit,
  ) {
    event.preventDefault();
    void callback(values);
  }

  return (
    <form className="space-y-5" onSubmit={(event) => submit(event, onSubmit)}>
      <div className="grid gap-4 sm:grid-cols-2">
        {!editing ? (
          <>
            <TextField
              label="Name"
              value={values.name}
              onChange={(value) => update("name", value)}
              required
            />
            <label className="space-y-2 text-sm font-medium">
              Engine
              <select
                className="h-9 w-full rounded-md border bg-background px-3 text-sm"
                onChange={(event) =>
                  update(
                    "engine",
                    event.target.value as ConnectionSummary["engine"],
                  )
                }
                value={values.engine}
              >
                <option value="postgres">PostgreSQL</option>
                <option value="mysql">MySQL</option>
                <option value="mariadb">MariaDB</option>
              </select>
            </label>
            <TextField
              label="Host"
              value={values.host}
              onChange={(value) => update("host", value)}
              required
            />
            <NumberField
              label="Port"
              min={1}
              value={values.port}
              onChange={(value) => update("port", value)}
            />
            <TextField
              label="Database"
              value={values.database}
              onChange={(value) => update("database", value)}
              required
            />
            <TextField
              label="Username"
              value={values.username}
              onChange={(value) => update("username", value)}
              required
            />
            <TextField
              autoComplete="new-password"
              label="Password"
              type="password"
              value={values.password}
              onChange={(value) => update("password", value)}
              required
            />
            <TextField
              label="SSL mode"
              value={values.sslMode}
              onChange={(value) => update("sslMode", value)}
            />
          </>
        ) : null}
        <NumberField
          label="Max open connections"
          value={values.maxOpenConnections}
          onChange={(value) => update("maxOpenConnections", value)}
        />
        <NumberField
          label="Max idle connections"
          value={values.maxIdleConnections}
          onChange={(value) => update("maxIdleConnections", value)}
        />
        <NumberField
          label="Max lifetime (seconds)"
          min={0}
          value={values.maxLifetimeSeconds}
          onChange={(value) => update("maxLifetimeSeconds", value)}
        />
        <NumberField
          label="Max idle time (seconds)"
          min={0}
          value={values.maxIdleTimeSeconds}
          onChange={(value) => update("maxIdleTimeSeconds", value)}
        />
        {!editing ? (
          <NumberField
            label="Connection timeout (seconds)"
            value={values.connectionTimeoutSeconds}
            onChange={(value) => update("connectionTimeoutSeconds", value)}
          />
        ) : null}
      </div>
      <div className="flex flex-wrap justify-end gap-2">
        {onTest ? (
          <Button
            disabled={busy}
            onClick={() => void onTest(values)}
            type="button"
            variant="outline"
          >
            Test connection
          </Button>
        ) : null}
        <Button disabled={busy} type="submit">
          {busy ? "Saving…" : editing ? "Save changes" : "Create connection"}
        </Button>
      </div>
    </form>
  );
}

export type GroupSummary = {
  id: string;
  name: string;
  leaderName: string;
  memberCount: number;
  memberIds?: string[];
  system?: boolean;
};

export function GroupList({
  groups,
  busy,
  onEdit,
}: {
  groups: GroupSummary[];
  busy: boolean;
  onEdit: (group: GroupSummary) => void;
}) {
  const columns: DataTableColumn<GroupSummary>[] = [
    { id: "name", header: "Group", cell: (group) => group.name },
    { id: "leader", header: "Leader", cell: (group) => group.leaderName },
    { id: "members", header: "Members", cell: (group) => group.memberCount },
    {
      id: "actions",
      header: "Actions",
      cell: (group) => (
        <div className="flex items-center gap-2">
          <Button disabled={busy} onClick={() => onEdit(group)} size="sm" variant="outline">
            Manage
          </Button>
          {group.system ? (
            <span className="text-xs text-muted-foreground">System group</span>
          ) : null}
        </div>
      ),
    },
  ];
  return (
    <DataTable columns={columns} getRowId={(group) => group.id} rows={groups} />
  );
}

export type MemberOption = { id: string; label: string };

export function MemberPicker({
  members,
  selectedIds,
  onSelectionChange,
  label = "Group members",
}: {
  members: MemberOption[];
  selectedIds: string[];
  onSelectionChange: (ids: string[]) => void;
  label?: string;
}) {
  const [search, setSearch] = useState("");
  const filtered = members.filter((member) =>
    member.label.toLocaleLowerCase().includes(search.toLocaleLowerCase()),
  );

  function toggle(id: string) {
    onSelectionChange(
      selectedIds.includes(id)
        ? selectedIds.filter((selectedId) => selectedId !== id)
        : [...selectedIds, id],
    );
  }

  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">{label}</legend>
      <div className="relative">
        <Search
          aria-hidden="true"
          className="absolute left-3 top-2.5 size-4 text-muted-foreground"
        />
        <Input
          aria-label="Search users"
          className="pl-9"
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Search users"
          value={search}
        />
      </div>
      <ul className="max-h-64 space-y-1 overflow-y-auto rounded-md border p-2">
        {filtered.map((member) => (
          <li key={member.id}>
            <label className="flex cursor-pointer items-center gap-3 rounded px-2 py-2 text-sm hover:bg-muted">
              <input
                checked={selectedIds.includes(member.id)}
                onChange={() => toggle(member.id)}
                type="checkbox"
              />
              {member.label}
            </label>
          </li>
        ))}
        {filtered.length === 0 ? (
          <li className="px-2 py-3 text-sm text-muted-foreground">
            No matching users.
          </li>
        ) : null}
      </ul>
    </fieldset>
  );
}

export function GroupLeaderSelect({
  members,
  leaderId,
  onLeaderChange,
}: {
  members: MemberOption[];
  leaderId: string;
  onLeaderChange: (id: string) => void;
}) {
  return (
    <label className="block space-y-2 text-sm font-medium">
      Group leader
      <select
        className="h-9 w-full rounded-md border bg-background px-3 text-sm"
        disabled={members.length === 0}
        onChange={(event) => onLeaderChange(event.target.value)}
        required
        value={leaderId}
      >
        <option value="">Choose an active member</option>
        {members.map((member) => (
          <option key={member.id} value={member.id}>{member.label}</option>
        ))}
      </select>
    </label>
  );
}

export function GroupEditor({
  name,
  members,
  selectedMemberIds,
  leaderId,
  busy = false,
  onNameChange,
  onMembersChange,
  onLeaderChange,
  onSubmit,
}: {
  name: string;
  members: (MemberOption & { active: boolean })[];
  selectedMemberIds: string[];
  leaderId: string;
  busy?: boolean;
  onNameChange: (name: string) => void;
  onMembersChange: (ids: string[]) => void;
  onLeaderChange: (id: string) => void;
  onSubmit: () => void;
}) {
  const eligibleLeaders = members.filter((member) =>
    member.active && selectedMemberIds.includes(member.id),
  );
  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <fieldset className="space-y-5" disabled={busy}>
        <div className="space-y-2">
          <Label htmlFor="group-name">Group name</Label>
          <Input
            id="group-name"
            onChange={(event) => onNameChange(event.target.value)}
            required
            value={name}
          />
        </div>
        <MemberPicker
          members={members}
          onSelectionChange={onMembersChange}
          selectedIds={selectedMemberIds}
        />
        <GroupLeaderSelect
          leaderId={leaderId}
          members={eligibleLeaders}
          onLeaderChange={onLeaderChange}
        />
        <Button
          disabled={busy || !name.trim() || !eligibleLeaders.some((member) => member.id === leaderId)}
          type="submit"
        >
          {busy ? "Saving…" : "Create group"}
        </Button>
      </fieldset>
    </form>
  );
}

export type AccessGrant = {
  id: string;
  groupId: string;
  connectionId: string;
  level: "view" | "view_edit" | null;
};

export function GroupAccessEditor({
  groups,
  connections,
  grants,
  onChange,
  disabled = false,
}: {
  groups: Array<{ id: string; name: string }>;
  connections: Array<{ id: string; name: string; driver: string }>;
  grants: AccessGrant[];
  onChange: (
    groupId: string,
    connectionId: string,
    level: AccessGrant["level"],
  ) => void;
  disabled?: boolean;
}) {
  const [selectedGroupId, setSelectedGroupId] = useState("");
  const [search, setSearch] = useState("");
  const [grantedOnly, setGrantedOnly] = useState(false);
  const [page, setPage] = useState(1);
  const pageSize = 10;
  const selectedGroup =
    groups.find((group) => group.id === selectedGroupId) ?? groups[0];
  const selectedGrants = useMemo(
    () =>
      new Map(
        grants
          .filter((grant) => grant.groupId === selectedGroup?.id)
          .map((grant) => [grant.connectionId, grant.level]),
      ),
    [grants, selectedGroup?.id],
  );
  const grantedCount = selectedGrants.size;
  const normalizedSearch = search.trim().toLocaleLowerCase();
  const filteredConnections = connections.filter((connection) => {
    const matchesSearch =
      normalizedSearch.length === 0 ||
      connection.name.toLocaleLowerCase().includes(normalizedSearch) ||
      connection.driver.toLocaleLowerCase().includes(normalizedSearch);
    const matchesGrantFilter =
      !grantedOnly || selectedGrants.has(connection.id);
    return matchesSearch && matchesGrantFilter;
  });
  const pageCount = Math.max(1, Math.ceil(filteredConnections.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  const visibleConnections = filteredConnections.slice(
    (currentPage - 1) * pageSize,
    currentPage * pageSize,
  );

  if (groups.length === 0 || connections.length === 0 || !selectedGroup) {
    return (
      <EmptyState
        description="Create at least one group and one connection before assigning access."
        title="No access grants available"
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <label className="space-y-2 text-sm font-medium">
          Group
          <select
            className="h-9 w-full rounded-md border bg-background px-3 text-sm"
            disabled={disabled}
            onChange={(event) => {
              setSelectedGroupId(event.target.value);
              setPage(1);
            }}
            value={selectedGroup.id}
          >
            {groups.map((group) => (
              <option key={group.id} value={group.id}>{group.name}</option>
            ))}
          </select>
        </label>
        <label className="space-y-2 text-sm font-medium">
          Search connections
          <Input
            onChange={(event) => {
              setSearch(event.target.value);
              setPage(1);
            }}
            placeholder="Search by name or database type"
            type="search"
            value={search}
          />
        </label>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-sm">
          <input
            checked={grantedOnly}
            disabled={disabled}
            onChange={(event) => {
              setGrantedOnly(event.target.checked);
              setPage(1);
            }}
            type="checkbox"
          />
          Show only connections with access
        </label>
        <p className="text-sm text-muted-foreground" aria-live="polite">
          {grantedCount} of {connections.length} connections have access for{" "}
          {selectedGroup.name}
        </p>
      </div>
      {visibleConnections.length > 0 ? (
        <ul className="divide-y rounded-lg border bg-background">
          {visibleConnections.map((connection) => {
            const level = selectedGrants.get(connection.id) ?? null;
            const controlId = `grant-${selectedGroup.id}-${connection.id}`;
            return (
              <li
                className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between"
                key={connection.id}
              >
                <div className="min-w-0">
                  <p className="truncate font-medium">{connection.name}</p>
                  <p className="text-sm text-muted-foreground">
                    {connection.driver}
                    {level ? (
                      <>
                        {" · "}
                        <AccessBadge level={level} />
                      </>
                    ) : (
                      " · No access"
                    )}
                  </p>
                </div>
                <label className="sr-only" htmlFor={controlId}>
                  {selectedGroup.name} access to {connection.name}
                </label>
                <select
                  className="h-9 w-full rounded-md border bg-background px-2 sm:w-44"
                  disabled={disabled}
                  id={controlId}
                  onChange={(event) =>
                    onChange(
                      selectedGroup.id,
                      connection.id,
                      event.target.value === "view"
                        ? "view"
                        : event.target.value === "view_edit"
                          ? "view_edit"
                          : null,
                    )
                  }
                  value={level ?? ""}
                >
                  <option value="">No access</option>
                  <option value="view">View</option>
                  <option value="view_edit">View + Edit</option>
                </select>
              </li>
            );
          })}
        </ul>
      ) : (
        <EmptyState
          description="Try a different search, or turn off the granted-only filter."
          title="No connections match"
        />
      )}
      {filteredConnections.length > pageSize ? (
        <PaginationControls
          disabled={disabled}
          onPageChange={setPage}
          page={currentPage}
          pageSize={pageSize}
          total={filteredConnections.length}
        />
      ) : null}
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  type = "text",
  autoComplete,
  required = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  autoComplete?: string;
  required?: boolean;
}) {
  return (
    <label className="space-y-2 text-sm font-medium">
      {label}
      <Input
        autoComplete={autoComplete}
        onChange={(event) => onChange(event.target.value)}
        required={required}
        type={type}
        value={value}
      />
    </label>
  );
}

function NumberField({
  label,
  value,
  onChange,
  min = 0,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
  min?: number;
}) {
  return (
    <label className="space-y-2 text-sm font-medium">
      {label}
      <Input
        min={min}
        onChange={(event) => onChange(Number(event.target.value))}
        required
        type="number"
        value={value}
      />
    </label>
  );
}
