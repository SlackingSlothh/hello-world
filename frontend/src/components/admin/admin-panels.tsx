"use client";

import { useState, type FormEvent, type ReactNode } from "react";
import { ArrowRightLeft, Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { DataTable, type DataTableColumn } from "@/components/shared/data-table";
import { EmptyState } from "@/components/shared/async-states";
import { AccessBadge, StatusBadge } from "@/components/shared/status-badge";

export type ConnectionSummary = {
  id: string;
  name: string;
  engine: "postgres" | "mysql" | "mariadb";
  host: string;
  port: number;
  database: string;
  status: string;
  activeConnections?: number;
  idleConnections?: number;
  lastChecked?: string;
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
            {connection.engine} · {connection.host}:{connection.port}/{connection.database}
          </p>
        </div>
        <StatusBadge status={connection.status} />
      </CardHeader>
      <CardContent className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
        <span>
          Pool: {connection.activeConnections ?? "—"} active,{" "}
          {connection.idleConnections ?? "—"} idle
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
}: {
  connections: ConnectionSummary[];
  onEdit?: (connection: ConnectionSummary) => void;
  onConnect?: (connection: ConnectionSummary) => void;
  onDisconnect?: (connection: ConnectionSummary) => void;
  onDelete?: (connection: ConnectionSummary) => void;
}) {
  const columns: DataTableColumn<ConnectionSummary>[] = [
    { id: "name", header: "Connection", cell: (item) => item.name },
    { id: "engine", header: "Engine", cell: (item) => item.engine },
    {
      id: "address",
      header: "Host / database",
      cell: (item) => `${item.host}:${item.port} / ${item.database}`,
    },
    { id: "status", header: "Status", cell: (item) => <StatusBadge status={item.status} /> },
    {
      id: "pool",
      header: "Pool",
      cell: (item) =>
        `${item.activeConnections ?? "—"} active · ${item.idleConnections ?? "—"} idle`,
    },
    {
      id: "actions",
      header: "Actions",
      cell: (item) => (
        <div className="flex flex-wrap gap-1">
          {onEdit ? (
            <Button onClick={() => onEdit(item)} size="sm" variant="outline">
              Edit
            </Button>
          ) : null}
          {item.status === "connected" ? (
            onDisconnect ? (
              <Button onClick={() => onDisconnect(item)} size="sm" variant="outline">
                Disconnect
              </Button>
            ) : null
          ) : onConnect ? (
            <Button onClick={() => onConnect(item)} size="sm" variant="outline">
              Connect
            </Button>
          ) : null}
          {onDelete ? (
            <Button
              aria-label={`Delete ${item.name}`}
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
  connectionTimeoutSeconds: number;
  statementTimeoutSeconds: number;
};

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
  connectionTimeoutSeconds: 10,
  statementTimeoutSeconds: 30,
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

  function submit(event: FormEvent<HTMLFormElement>, callback: typeof onSubmit) {
    event.preventDefault();
    void callback(values);
  }

  return (
    <form className="space-y-5" onSubmit={(event) => submit(event, onSubmit)}>
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField label="Name" value={values.name} onChange={(value) => update("name", value)} required />
        <label className="space-y-2 text-sm font-medium">
          Engine
          <select
            className="h-9 w-full rounded-md border bg-background px-3 text-sm"
            onChange={(event) =>
              update("engine", event.target.value as ConnectionSummary["engine"])
            }
            value={values.engine}
          >
            <option value="postgres">PostgreSQL</option>
            <option value="mysql">MySQL</option>
            <option value="mariadb">MariaDB</option>
          </select>
        </label>
        <TextField label="Host" value={values.host} onChange={(value) => update("host", value)} required />
        <NumberField label="Port" value={values.port} onChange={(value) => update("port", value)} />
        <TextField label="Database" value={values.database} onChange={(value) => update("database", value)} required />
        <TextField label="Username" value={values.username} onChange={(value) => update("username", value)} required />
        <TextField
          autoComplete="new-password"
          label={editing ? "Password (leave blank to keep current)" : "Password"}
          type="password"
          value={values.password}
          onChange={(value) => update("password", value)}
          required={!editing}
        />
        <TextField label="SSL mode" value={values.sslMode} onChange={(value) => update("sslMode", value)} />
        <NumberField label="Max open connections" value={values.maxOpenConnections} onChange={(value) => update("maxOpenConnections", value)} />
        <NumberField label="Max idle connections" value={values.maxIdleConnections} onChange={(value) => update("maxIdleConnections", value)} />
        <NumberField label="Connection timeout (seconds)" value={values.connectionTimeoutSeconds} onChange={(value) => update("connectionTimeoutSeconds", value)} />
        <NumberField label="Statement timeout (seconds)" value={values.statementTimeoutSeconds} onChange={(value) => update("statementTimeoutSeconds", value)} />
      </div>
      <div className="flex flex-wrap justify-end gap-2">
        {onTest ? (
          <Button disabled={busy} onClick={() => void onTest(values)} type="button" variant="outline">
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
  onEdit,
  onDelete,
}: {
  groups: GroupSummary[];
  onEdit?: (group: GroupSummary) => void;
  onDelete?: (group: GroupSummary) => void;
}) {
  const columns: DataTableColumn<GroupSummary>[] = [
    { id: "name", header: "Group", cell: (group) => group.name },
    { id: "leader", header: "Leader", cell: (group) => group.leaderName },
    { id: "members", header: "Members", cell: (group) => group.memberCount },
    {
      id: "actions",
      header: "Actions",
      cell: (group) => (
        <div className="flex gap-2">
          {onEdit ? (
            <Button onClick={() => onEdit(group)} size="sm" variant="outline">
              Manage
            </Button>
          ) : null}
          {onDelete && !group.system ? (
            <Button
              aria-label={`Delete ${group.name}`}
              onClick={() => onDelete(group)}
              size="icon-sm"
              variant="destructive"
            >
              <Trash2 aria-hidden="true" />
            </Button>
          ) : group.system ? (
            <span className="text-xs text-muted-foreground">System group</span>
          ) : null}
        </div>
      ),
    },
  ];
  return <DataTable columns={columns} getRowId={(group) => group.id} rows={groups} />;
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
        <Search aria-hidden="true" className="absolute left-3 top-2.5 size-4 text-muted-foreground" />
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
          <li className="px-2 py-3 text-sm text-muted-foreground">No matching users.</li>
        ) : null}
      </ul>
    </fieldset>
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
  members: MemberOption[];
  selectedMemberIds: string[];
  leaderId: string;
  busy?: boolean;
  onNameChange: (name: string) => void;
  onMembersChange: (ids: string[]) => void;
  onLeaderChange: (id: string) => void;
  onSubmit: () => void;
}) {
  const selectedMembers = members.filter((member) => selectedMemberIds.includes(member.id));
  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
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
      <label className="block space-y-2 text-sm font-medium">
        Group leader
        <select
          className="h-9 w-full rounded-md border bg-background px-3 text-sm"
          onChange={(event) => onLeaderChange(event.target.value)}
          required
          value={leaderId}
        >
          <option value="">Choose a member</option>
          {selectedMembers.map((member) => (
            <option key={member.id} value={member.id}>
              {member.label}
            </option>
          ))}
        </select>
      </label>
      <Button
        disabled={
          busy ||
          selectedMembers.length === 0 ||
          !selectedMemberIds.includes(leaderId)
        }
        type="submit"
      >
        {busy ? "Saving…" : "Save group"}
      </Button>
    </form>
  );
}

export type AccessGrant = {
  id: string;
  groupId: string;
  connectionId: string;
  level: "view" | "view_edit" | null;
};

export function AccessGrantMatrix({
  groups,
  connections,
  grants,
  onChange,
  disabled = false,
}: {
  groups: Array<{ id: string; name: string }>;
  connections: Array<{ id: string; name: string }>;
  grants: AccessGrant[];
  onChange: (groupId: string, connectionId: string, level: AccessGrant["level"]) => void;
  disabled?: boolean;
}) {
  const levelFor = (groupId: string, connectionId: string) =>
    grants.find((grant) => grant.groupId === groupId && grant.connectionId === connectionId)?.level ??
    "";

  if (groups.length === 0 || connections.length === 0) {
    return (
      <EmptyState
        description="Create at least one group and one connection before assigning access."
        title="Access matrix is empty"
      />
    );
  }

  return (
    <div className="w-full overflow-x-auto rounded-lg border bg-background">
      <table className="w-full min-w-[640px] text-sm">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="p-3 text-left font-medium">Group</th>
            {connections.map((connection) => (
              <th className="p-3 text-left font-medium" key={connection.id}>
                {connection.name}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {groups.map((group) => (
            <tr className="border-b last:border-0" key={group.id}>
              <th className="p-3 text-left font-medium">{group.name}</th>
              {connections.map((connection) => {
                const level = levelFor(group.id, connection.id);
                return (
                  <td className="p-3" key={connection.id}>
                    <label className="sr-only" htmlFor={`grant-${group.id}-${connection.id}`}>
                      {group.name} access to {connection.name}
                    </label>
                    <select
                      className="h-9 w-full min-w-32 rounded-md border bg-background px-2"
                      disabled={disabled}
                      id={`grant-${group.id}-${connection.id}`}
                      onChange={(event) =>
                        onChange(
                          group.id,
                          connection.id,
                          (event.target.value || null) as AccessGrant["level"],
                        )
                      }
                      value={level}
                    >
                      <option value="">No access</option>
                      <option value="view">View</option>
                      <option value="view_edit">View + Edit</option>
                    </select>
                    {level ? (
                      <span className="sr-only">
                        Current level: <AccessBadge level={level} />
                      </span>
                    ) : null}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function AdminModeSwitcher({
  mode,
  busy = false,
  onSwitch,
}: {
  mode: "user" | "admin";
  busy?: boolean;
  onSwitch: (mode: "user" | "admin") => void;
}) {
  const nextMode = mode === "admin" ? "user" : "admin";
  return (
    <Button disabled={busy} onClick={() => onSwitch(nextMode)} variant="outline">
      <ArrowRightLeft aria-hidden="true" />
      {busy ? "Switching…" : `Switch to ${nextMode} mode`}
    </Button>
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
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <label className="space-y-2 text-sm font-medium">
      {label}
      <Input
        min={0}
        onChange={(event) => onChange(Number(event.target.value))}
        required
        type="number"
        value={value}
      />
    </label>
  );
}
