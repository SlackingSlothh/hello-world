"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import {
  DraftEditor,
  type DraftFormValues,
} from "@/components/migration/migration-panels";
import {
  DataTable,
  type DataTableColumn,
} from "@/components/shared/data-table";
import { StatusBadge } from "@/components/shared/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  createMigrationDraft,
  submitMigrationRequest,
  type MigrationDefinition,
  type MigrationHistoryEntry,
  type MigrationOperation,
} from "@/lib/api/migrations";
import { availableMigrationOperations } from "@/lib/migration-operations";

export function MigrationActions({
  connectionId,
  migrations,
  runs,
  canSubmit,
  onDraftCreated,
}: {
  connectionId: string;
  migrations: MigrationDefinition[];
  runs: MigrationHistoryEntry[];
  canSubmit: boolean;
  onDraftCreated: (draft: MigrationDefinition) => void;
}) {
  const router = useRouter();
  const [showDraft, setShowDraft] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operations = availableMigrationOperations(migrations, runs);

  async function saveDraft(draft: DraftFormValues) {
    setBusy(true);
    setError("");
    try {
      const saved = await createMigrationDraft(connectionId, {
        title: draft.name,
        description: draft.description,
        sql_content: draft.upSql,
        execution_mode: draft.mode === "batch" ? "batch" : "normal",
        ...(draft.mode === "batch"
          ? {
              batch_by_attribute: draft.cursorColumn,
              batch_size: draft.batchSize,
            }
          : {}),
        ...(draft.irreversible
          ? { irreversible_reason: draft.irreversibleReason }
          : { sql_down: draft.downSql }),
      });
      onDraftCreated(saved);
      setShowDraft(false);
    } catch (cause) {
      setError(
        cause instanceof Error && cause.name !== "ZodError"
          ? cause.message
          : "Could not save the draft.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function submit(
    migration: MigrationDefinition,
    operation: MigrationOperation,
  ) {
    setBusy(true);
    setError("");
    try {
      const request = await submitMigrationRequest(
        connectionId,
        migration.id,
        migration.revision,
        operation,
      );
      router.push(
        `/migration/${encodeURIComponent(connectionId)}/requests/${request.id}`,
      );
    } catch (cause) {
      setError(
        cause instanceof Error && cause.name !== "ZodError"
          ? cause.message
          : "Could not submit the migration request.",
      );
      setBusy(false);
    }
  }

  const columns: DataTableColumn<MigrationDefinition>[] = [
    { id: "title", header: "Migration", cell: (item) => item.title },
    {
      id: "mode",
      header: "Mode",
      cell: (item) => (
        <div className="text-xs">
          <div className="font-medium">{item.execution_mode ?? "normal"}</div>
          {item.execution_mode === "batch" ? (
            <div className="text-muted-foreground">
              {item.batch_by_attribute ?? "—"} · size {item.batch_size ?? "—"}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      id: "version",
      header: "Version",
      cell: (item) => (item.version == null ? "—" : `v${item.version}`),
    },
    { id: "revision", header: "Revision", cell: (item) => item.revision },
    {
      id: "status",
      header: "Status",
      cell: (item) => <StatusBadge status={item.status} />,
    },
  ];
  if (canSubmit) {
    columns.push({
      id: "actions",
      header: "Request",
      cell: (item) => {
        const operation = operations.get(item.id);
        return operation ? (
          <Button
            disabled={busy}
            size="sm"
            variant="outline"
            onClick={() => void submit(item, operation)}
          >
            Request {operation.toLowerCase()}
          </Button>
        ) : null;
      },
    });
  }

  return (
    <section aria-labelledby="migration-definitions" className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="migration-definitions" className="text-lg font-semibold">
          Migration definitions
        </h2>
        {canSubmit && !showDraft && (
          <Button disabled={busy} onClick={() => setShowDraft(true)}>
            New draft
          </Button>
        )}
      </div>
      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      {canSubmit && showDraft && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-3">
            <CardTitle>New draft</CardTitle>
            <Button
              disabled={busy}
              onClick={() => setShowDraft(false)}
              size="sm"
              variant="outline"
            >
              Cancel
            </Button>
          </CardHeader>
          <CardContent>
            <DraftEditor allowBatch busy={busy} onSave={saveDraft} />
          </CardContent>
        </Card>
      )}
      <DataTable
        columns={columns}
        emptyTitle="No migration definitions"
        getRowId={(item) => String(item.id)}
        rows={migrations}
      />
    </section>
  );
}
