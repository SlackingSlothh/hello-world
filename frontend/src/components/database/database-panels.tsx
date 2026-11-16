"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { useState } from "react";
import { ChevronDown, ChevronRight, Database, Play, Square } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { AccessBadge, StatusBadge } from "@/components/shared/status-badge";
import { EmptyState } from "@/components/shared/async-states";
import { DataTable, type DataTableColumn } from "@/components/shared/data-table";
import { PaginationControls } from "@/components/shared/pagination-controls";

export type DatabaseSummary = {
  id: string;
  name: string;
  engine: "postgres" | "mysql" | "mariadb";
  status: string;
  access: "view" | "view_edit";
  migrationRunning?: boolean;
  awaitingReview?: boolean;
};

export function DatabaseCard({ database }: { database: DatabaseSummary }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="rounded-md bg-primary/10 p-2 text-primary">
            <Database aria-hidden="true" className="size-5" />
          </span>
          <div>
            <CardTitle>{database.name}</CardTitle>
            <p className="mt-1 text-sm capitalize text-muted-foreground">{database.engine}</p>
          </div>
        </div>
        <StatusBadge status={database.status} />
      </CardHeader>
      <CardContent className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <AccessBadge level={database.access} />
          {database.migrationRunning ? <StatusBadge status="running" /> : null}
          {database.awaitingReview ? <StatusBadge status="pending approval" /> : null}
        </div>
        <Link
          className="inline-flex h-8 items-center justify-center rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          href={`/db/${encodeURIComponent(database.id)}`}
        >
          Open database
        </Link>
      </CardContent>
    </Card>
  );
}

export function DatabaseList({
  databases,
  emptyTitle = "No databases available",
  emptyDescription = "An administrator can grant database access through a group.",
}: {
  databases: DatabaseSummary[];
  emptyTitle?: string;
  emptyDescription?: string;
}) {
  if (databases.length === 0) {
    return <EmptyState description={emptyDescription} title={emptyTitle} />;
  }
  return (
    <ul className="grid gap-4 md:grid-cols-2">
      {databases.map((database) => (
        <li key={database.id}>
          <DatabaseCard database={database} />
        </li>
      ))}
    </ul>
  );
}

export type WorkspaceTab = "schema" | "query" | "migration";

export function DatabaseWorkspaceShell({
  database,
  activeTab,
  children,
}: {
  database: DatabaseSummary;
  activeTab: WorkspaceTab;
  children: ReactNode;
}) {
  const tabs: Array<{ id: WorkspaceTab; label: string }> = [
    { id: "schema", label: "Schema" },
    { id: "query", label: "Query" },
    ...(database.engine === "postgres" ? [{ id: "migration" as const, label: "Migration" }] : []),
  ];

  return (
    <section className="space-y-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-sm text-muted-foreground">Database workspace</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">{database.name}</h1>
          <p className="mt-1 text-sm capitalize text-muted-foreground">{database.engine}</p>
        </div>
        <StatusBadge status={database.status} />
      </header>
      <nav aria-label="Database sections" className="flex flex-wrap gap-2 border-b">
        {tabs.map((tab) => (
          <Link
            aria-current={activeTab === tab.id ? "page" : undefined}
            className={`border-b-2 px-3 py-2 text-sm ${
              activeTab === tab.id
                ? "border-primary font-medium text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
            href={`/db/${encodeURIComponent(database.id)}?tab=${tab.id}`}
            key={tab.id}
            scroll={false}
          >
            {tab.label}
          </Link>
        ))}
      </nav>
      <div>{children}</div>
    </section>
  );
}

export type SchemaNode = {
  name: string;
  tables: string[];
};

export function SchemaTree({
  schemas,
  selectedSchema,
  selectedTable,
  onSelect,
  singleLevel = false,
}: {
  schemas: SchemaNode[];
  selectedSchema?: string;
  selectedTable?: string;
  onSelect: (schema: string, table: string) => void;
  singleLevel?: boolean;
}) {
  const [expanded, setExpanded] = useState<string[]>(selectedSchema ? [selectedSchema] : []);
  if (schemas.length === 0) {
    return <EmptyState title="No schemas found" />;
  }

  function toggle(schema: string) {
    setExpanded((current) =>
      current.includes(schema)
        ? current.filter((value) => value !== schema)
        : [...current, schema],
    );
  }

  return (
    <nav aria-label="Database schemas" className="rounded-lg border bg-background p-2">
      <ul className="space-y-1">
        {schemas.map((schema) => {
          const isExpanded = expanded.includes(schema.name);
          const panelId = `schema-${schema.name.replaceAll(/[^a-zA-Z0-9_-]/g, "-")}`;
          return (
            <li key={schema.name}>
              <button
                aria-controls={panelId}
                aria-expanded={isExpanded}
                className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm font-medium hover:bg-muted"
                onClick={() => toggle(schema.name)}
                type="button"
              >
                {isExpanded ? (
                  <ChevronDown aria-hidden="true" className="size-4" />
                ) : (
                  <ChevronRight aria-hidden="true" className="size-4" />
                )}
                {singleLevel ? "Database" : "Schema"}: {schema.name}
              </button>
              {isExpanded ? (
                <ul className="ml-5 border-l pl-3" id={panelId}>
                  {schema.tables.map((table) => (
                    <li key={table}>
                      <button
                        aria-current={
                          selectedSchema === schema.name && selectedTable === table
                            ? "page"
                            : undefined
                        }
                        className="w-full rounded-md px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-muted hover:text-foreground aria-[current=page]:bg-muted aria-[current=page]:font-medium aria-[current=page]:text-foreground"
                        onClick={() => onSelect(schema.name, table)}
                        type="button"
                      >
                        {table}
                      </button>
                    </li>
                  ))}
                  {schema.tables.length === 0 ? (
                    <li className="px-2 py-2 text-xs text-muted-foreground">No tables</li>
                  ) : null}
                </ul>
              ) : null}
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

export type TableDetailTab = "data" | "columns" | "constraints" | "indices";

export function TableDetailsTabs({
  activeTab,
  onTabChange,
  children,
}: {
  activeTab: TableDetailTab;
  onTabChange: (tab: TableDetailTab) => void;
  children: ReactNode;
}) {
  const tabs: Array<{ id: TableDetailTab; label: string }> = [
    { id: "data", label: "Data" },
    { id: "columns", label: "Columns" },
    { id: "constraints", label: "Constraints" },
    { id: "indices", label: "Indices" },
  ];
  return (
    <section className="space-y-4">
      <div aria-label="Table details" className="flex flex-wrap gap-1 border-b" role="tablist">
        {tabs.map((tab) => (
          <button
            aria-selected={activeTab === tab.id}
            className={`border-b-2 px-3 py-2 text-sm ${
              activeTab === tab.id
                ? "border-primary font-medium"
                : "border-transparent text-muted-foreground"
            }`}
            key={tab.id}
            onClick={() => onTabChange(tab.id)}
            role="tab"
            type="button"
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div role="tabpanel">{children}</div>
    </section>
  );
}

export function TableDataGrid<T>({
  columns,
  rows,
  getRowId,
  page,
  pageSize,
  total,
  onPageChange,
  onSort,
}: {
  columns: DataTableColumn<T>[];
  rows: T[];
  getRowId: (row: T) => string;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onSort?: (columnId: string) => void;
}) {
  const sortableColumns = columns.map((column) => ({
    ...column,
    header: onSort ? (
      <button
        className="font-medium hover:underline"
        onClick={() => onSort(column.id)}
        type="button"
      >
        {column.header}
      </button>
    ) : (
      column.header
    ),
  }));
  return (
    <div className="space-y-4">
      <DataTable
        caption="Paginated table data"
        columns={sortableColumns}
        emptyTitle="No rows"
        getRowId={getRowId}
        rows={rows}
      />
      <PaginationControls
        onPageChange={onPageChange}
        page={page}
        pageSize={pageSize}
        total={total}
      />
    </div>
  );
}

export function SqlEditor({
  value,
  onChange,
  onRun,
  onCancel,
  running = false,
  disabled = false,
}: {
  value: string;
  onChange: (value: string) => void;
  onRun: () => void;
  onCancel?: () => void;
  running?: boolean;
  disabled?: boolean;
}) {
  return (
    <section className="space-y-3">
      <label className="block space-y-2 text-sm font-medium" htmlFor="sql-query">
        Read-only SQL query
        <Textarea
          aria-describedby="sql-query-hint"
          className="min-h-48 resize-y font-mono text-sm"
          disabled={disabled || running}
          id="sql-query"
          onChange={(event) => onChange(event.target.value)}
          placeholder="SELECT * FROM schema_name.table_name LIMIT 100;"
          spellCheck={false}
          value={value}
        />
      </label>
      <p className="text-xs text-muted-foreground" id="sql-query-hint">
        Run one read-only statement. The backend validates every query.
      </p>
      <div className="flex gap-2">
        <Button disabled={disabled || running || !value.trim()} onClick={onRun}>
          <Play aria-hidden="true" />
          Run query
        </Button>
        {running && onCancel ? (
          <Button onClick={onCancel} variant="outline">
            <Square aria-hidden="true" />
            Cancel query
          </Button>
        ) : null}
      </div>
    </section>
  );
}

export function QueryResults<T>({
  columns,
  rows,
  getRowId,
  elapsedMs,
  truncated = false,
}: {
  columns: DataTableColumn<T>[];
  rows: T[];
  getRowId: (row: T) => string;
  elapsedMs?: number;
  truncated?: boolean;
}) {
  return (
    <section aria-live="polite" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
        <span>{rows.length} rows returned{truncated ? " (results truncated)" : ""}</span>
        {elapsedMs !== undefined ? <span>Completed in {elapsedMs} ms</span> : null}
      </div>
      <DataTable
        caption="Read-only query results"
        columns={columns}
        emptyTitle="Query completed with no rows"
        getRowId={getRowId}
        rows={rows}
      />
    </section>
  );
}
