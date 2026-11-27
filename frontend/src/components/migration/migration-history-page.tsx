"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { FastForward, Play, Plus, RefreshCw, RotateCcw } from "lucide-react";
import { toast } from "sonner";
import {
  DraftEditor,
  JobList,
  JobProgress,
  MigrationHistory,
  toJobSummary,
  type DraftFormValues,
  type JobSummary,
} from "@/components/migration/migration-panels";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/components/shared/async-states";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import {
  DataTable,
  type DataTableColumn,
} from "@/components/shared/data-table";
import { PageHeader } from "@/components/shared/page-header";
import { StatusBadge } from "@/components/shared/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  createMigration,
  enqueueJobRecovery,
  enqueueMigrationAction,
  getMigrationJob,
  listMigrationConnections,
  listMigrationJobs,
  listMigrations,
  loadMigrationHistory,
  type MigrationConnection,
  type MigrationDefinition,
  type MigrationHistoryEntry,
  type MigrationJob,
} from "@/lib/api/migrations";

type WorkspaceTab = "migrations" | "jobs" | "history";

type WorkspaceData = {
  connection: MigrationConnection;
  runs: MigrationHistoryEntry[];
  migrations: MigrationDefinition[];
  jobs: MigrationJob[];
};

type PageData = { connections: MigrationConnection[] } | WorkspaceData;

type PendingRecovery = {
  job: JobSummary;
  action: "resume" | "revert";
};

const jobsPageSize = 10;

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error && error.name !== "ZodError"
    ? error.message
    : fallback;
}

export function MigrationHistoryPage({
  connectionId,
}: {
  connectionId?: string;
}) {
  const [data, setData] = useState<PageData>();
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [retry, setRetry] = useState(0);
  const [activeTab, setActiveTab] = useState<WorkspaceTab>("migrations");
  const [showDraftForm, setShowDraftForm] = useState(false);
  const [creatingDraft, setCreatingDraft] = useState(false);
  const [busyMigrationId, setBusyMigrationId] = useState<number>();
  const [busyJobId, setBusyJobId] = useState<string>();
  const [selectedJob, setSelectedJob] = useState<JobSummary>();
  const [pendingRecovery, setPendingRecovery] = useState<PendingRecovery>();
  const [jobsPage, setJobsPage] = useState(1);

  const loadWorkspace = useCallback(
    async (id: string, signal: AbortSignal): Promise<WorkspaceData> => {
      const [historyData, migrations, jobs] = await Promise.all([
        loadMigrationHistory(id, signal),
        listMigrations(id, signal),
        listMigrationJobs(id, signal, { limit: 100, offset: 0 }),
      ]);
      return {
        connection: historyData.connection,
        runs: historyData.runs,
        migrations,
        jobs,
      };
    },
    [],
  );

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    const request: Promise<PageData> = connectionId
      ? loadWorkspace(connectionId, signal)
      : listMigrationConnections(signal).then((connections) => ({
          connections,
        }));
    request.then(
      (result) => {
        if (signal.aborted) return;
        setData(result);
        setError("");
        if ("jobs" in result && result.jobs.length > 0) {
          setSelectedJob((current) => {
            if (!current) return toJobSummary(result.jobs[0]);
            const updated = result.jobs.find(
              (item) => item.job_id === current.id,
            );
            return updated ? toJobSummary(updated) : current;
          });
        }
      },
      (cause: unknown) => {
        if (signal.aborted) return;
        setError(errorMessage(cause, "Could not load migration data."));
      },
    );
    return () => controller.abort();
  }, [connectionId, loadWorkspace, retry]);

  const refreshWorkspace = useCallback(async () => {
    if (!connectionId) {
      setRetry((attempt) => attempt + 1);
      return;
    }
    const controller = new AbortController();
    try {
      const fresh = await loadWorkspace(connectionId, controller.signal);
      setData(fresh);
      setActionError("");
      setSelectedJob((current) => {
        if (!current) {
          return fresh.jobs[0] ? toJobSummary(fresh.jobs[0]) : undefined;
        }
        const match = fresh.jobs.find((item) => item.job_id === current.id);
        return match ? toJobSummary(match) : current;
      });
    } catch (cause) {
      setActionError(errorMessage(cause, "Could not refresh migration data."));
    }
  }, [connectionId, loadWorkspace]);

  const fetchJobSummary = useCallback(async (jobId: string) => {
    const job = await getMigrationJob(jobId);
    const summary = toJobSummary(job);
    setData((current) => {
      if (!current || !("jobs" in current)) return current;
      const exists = current.jobs.some((item) => item.job_id === job.job_id);
      const nextJobs = exists
        ? current.jobs.map((item) => (item.job_id === job.job_id ? job : item))
        : [job, ...current.jobs];
      return { ...current, jobs: nextJobs };
    });
    return summary;
  }, []);

  async function handleCreateDraft(values: DraftFormValues) {
    if (!connectionId) return;
    setCreatingDraft(true);
    setActionError("");
    try {
      const created = await createMigration(connectionId, {
        title: values.name,
        description: values.description,
        sql_content: values.upSql,
        sql_down: values.irreversible ? undefined : values.downSql,
        irreversible_reason: values.irreversible
          ? values.irreversibleReason
          : undefined,
        execution_mode: values.mode === "batch" ? "batch" : "normal",
        batch_by_attribute:
          values.mode === "batch" ? values.cursorColumn : undefined,
        batch_size: values.mode === "batch" ? values.batchSize : undefined,
      });
      setData((current) =>
        current && "migrations" in current
          ? { ...current, migrations: [created, ...current.migrations] }
          : current,
      );
      setShowDraftForm(false);
      toast.success(`Migration draft "${created.title}" created.`);
    } catch (cause) {
      const msg = errorMessage(cause, "Could not create migration draft.");
      setActionError(msg);
      toast.error(msg);
    } finally {
      setCreatingDraft(false);
    }
  }

  async function handleMigrationAction(
    migration: MigrationDefinition,
    action: "execute" | "rollback" | "forward",
  ) {
    if (!connectionId) return;
    setBusyMigrationId(migration.id);
    setActionError("");
    try {
      const enqueued = await enqueueMigrationAction(
        connectionId,
        migration.id,
        action,
      );
      const placeholder: JobSummary = {
        id: enqueued.job_id,
        executionId: enqueued.execution_id,
        migrationId: migration.id,
        action,
        mode: migration.execution_mode,
        status: enqueued.status,
      };
      setSelectedJob(placeholder);
      setActiveTab("jobs");
      toast.success(`Job ${enqueued.job_id} queued for ${action}.`);
      void refreshWorkspace();
    } catch (cause) {
      const msg = errorMessage(cause, `Could not enqueue ${action}.`);
      setActionError(msg);
      toast.error(msg);
    } finally {
      setBusyMigrationId(undefined);
    }
  }

  async function handleConfirmRecovery() {
    if (!connectionId || !pendingRecovery) return;
    const { job, action } = pendingRecovery;
    setBusyJobId(job.id);
    setActionError("");
    try {
      const enqueued = await enqueueJobRecovery(connectionId, job.id, action);
      const nextSummary: JobSummary = {
        id: enqueued.job_id,
        executionId: enqueued.execution_id,
        migrationId: job.migrationId,
        action: action === "resume" ? "resume_batch" : "revert_batch",
        mode: "batch",
        status: enqueued.status,
      };
      setSelectedJob(nextSummary);
      setPendingRecovery(undefined);
      toast.success(
        action === "resume"
          ? `Resume job ${enqueued.job_id} queued.`
          : `Revert job ${enqueued.job_id} queued.`,
      );
      void refreshWorkspace();
    } catch (cause) {
      const msg = errorMessage(
        cause,
        `Could not enqueue batch ${action} recovery.`,
      );
      setActionError(msg);
      toast.error(msg);
    } finally {
      setBusyJobId(undefined);
    }
  }

  const jobSummaries = useMemo(
    () => (data && "jobs" in data ? data.jobs.map(toJobSummary) : []),
    [data],
  );
  const pagedJobs = useMemo(() => {
    const start = (jobsPage - 1) * jobsPageSize;
    return jobSummaries.slice(start, start + jobsPageSize);
  }, [jobSummaries, jobsPage]);

  const migrationColumns: DataTableColumn<MigrationDefinition>[] = [
    {
      id: "title",
      header: "Migration",
      cell: (item) => (
        <div className="min-w-0">
          <div className="font-medium">{item.title}</div>
          <div className="text-xs text-muted-foreground">
            #{item.id}
            {item.version !== undefined ? ` · v${item.version}` : ""} · Rev{" "}
            {item.revision}
          </div>
          {item.description ? (
            <p className="mt-1 text-xs text-muted-foreground">
              {item.description}
            </p>
          ) : null}
        </div>
      ),
    },
    {
      id: "mode",
      header: "Mode",
      cell: (item) => (
        <div className="text-xs">
          <div className="font-medium">{item.execution_mode}</div>
          {item.execution_mode === "batch" ? (
            <div className="text-muted-foreground">
              {item.batch_by_attribute ?? "—"} · size {item.batch_size ?? "—"}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      id: "reversible",
      header: "Reversible",
      cell: (item) => (item.sql_down ? "Reversible" : "Irreversible"),
    },
    {
      id: "status",
      header: "Status",
      cell: (item) => <StatusBadge status={item.status} />,
    },
    {
      id: "actions",
      header: "Actions",
      cell: (item) => {
        const busy = busyMigrationId === item.id;
        return (
          <div className="flex flex-wrap gap-1">
            {item.status !== "APPLIED" ? (
              <Button
                disabled={busy}
                onClick={() => void handleMigrationAction(item, "execute")}
                size="sm"
              >
                <Play aria-hidden="true" />
                Execute
              </Button>
            ) : (
              <>
                {item.sql_down ? (
                  <Button
                    disabled={busy}
                    onClick={() => void handleMigrationAction(item, "rollback")}
                    size="sm"
                    variant="outline"
                  >
                    <RotateCcw aria-hidden="true" />
                    Rollback
                  </Button>
                ) : null}
                <Button
                  disabled={busy}
                  onClick={() => void handleMigrationAction(item, "forward")}
                  size="sm"
                  variant="outline"
                >
                  <FastForward aria-hidden="true" />
                  Forward
                </Button>
              </>
            )}
          </div>
        );
      },
    },
  ];

  const workspaceData = data && "runs" in data ? data : undefined;

  return (
    <div className="space-y-5">
      <PageHeader
        actions={
          workspaceData ? (
            <div className="flex flex-wrap gap-2">
              <Button
                onClick={() => void refreshWorkspace()}
                size="sm"
                variant="outline"
              >
                <RefreshCw aria-hidden="true" />
                Refresh
              </Button>
              <Button
                onClick={() => {
                  setActiveTab("migrations");
                  setShowDraftForm((open) => !open);
                }}
                size="sm"
              >
                <Plus aria-hidden="true" />
                {showDraftForm ? "Close draft form" : "New migration"}
              </Button>
            </div>
          ) : undefined
        }
        breadcrumbs={
          connectionId ? [{ label: "Migrations", href: "/migration" }] : undefined
        }
        description={
          workspaceData
            ? `${workspaceData.connection.driver} · ${workspaceData.connection.connected ? "Connected" : "Disconnected"}`
            : undefined
        }
        title={
          workspaceData
            ? `Migrations · ${workspaceData.connection.name}`
            : "Migrations"
        }
      />

      {actionError ? (
        <ErrorState message={actionError} title="Migration action failed" />
      ) : null}

      {error ? (
        <ErrorState
          message={error}
          onRetry={() => {
            setError("");
            setRetry((attempt) => attempt + 1);
          }}
        />
      ) : !data ? (
        <LoadingState
          label={connectionId ? "Loading migration workspace" : "Loading databases"}
        />
      ) : "runs" in data ? (
        <div className="space-y-5">
          <div
            aria-label="Migration workspace sections"
            className="flex flex-wrap gap-2 border-b pb-2"
            role="tablist"
          >
            {(
              [
                [
                  "migrations",
                  `Migrations (${data.migrations.length})`,
                ],
                ["jobs", `Async jobs (${data.jobs.length})`],
                ["history", `History (${data.runs.length})`],
              ] as const
            ).map(([tabId, label]) => (
              <Button
                aria-selected={activeTab === tabId}
                key={tabId}
                onClick={() => setActiveTab(tabId)}
                role="tab"
                size="sm"
                variant={activeTab === tabId ? "default" : "outline"}
              >
                {label}
              </Button>
            ))}
          </div>

          {activeTab === "migrations" ? (
            <div className="space-y-5">
              {showDraftForm ? (
                <Card>
                  <CardHeader>
                    <CardTitle>Create migration draft</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <DraftEditor
                      busy={creatingDraft}
                      onSave={handleCreateDraft}
                    />
                  </CardContent>
                </Card>
              ) : null}
              <DataTable
                columns={migrationColumns}
                emptyDescription="Create a normal or batch migration draft to queue execution."
                emptyTitle="No migrations defined"
                getRowId={(item) => String(item.id)}
                rows={data.migrations}
              />
            </div>
          ) : null}

          {activeTab === "jobs" ? (
            <div className="space-y-5">
              {selectedJob ? (
                <JobProgress
                  actionBusy={busyJobId === selectedJob.id}
                  fetchJob={fetchJobSummary}
                  initialJob={selectedJob}
                  key={selectedJob.id}
                  onResume={(job) =>
                    setPendingRecovery({ job, action: "resume" })
                  }
                  onRevert={(job) =>
                    setPendingRecovery({ job, action: "revert" })
                  }
                  onTerminal={() => void refreshWorkspace()}
                />
              ) : null}
              <JobList
                actionBusyJobId={busyJobId}
                jobs={pagedJobs}
                onPageChange={setJobsPage}
                onResume={(job) => setPendingRecovery({ job, action: "resume" })}
                onRevert={(job) => setPendingRecovery({ job, action: "revert" })}
                onSelect={setSelectedJob}
                page={jobsPage}
                pageSize={jobsPageSize}
                total={jobSummaries.length}
              />
            </div>
          ) : null}

          {activeTab === "history" ? (
            <MigrationHistory records={data.runs} />
          ) : null}
        </div>
      ) : data.connections.length === 0 ? (
        <EmptyState
          description="A database appears here when your group has access."
          title="No databases available"
        />
      ) : (
        <ul className="grid gap-4 md:grid-cols-2">
          {data.connections.map((connection) => (
            <li key={connection.id} className="min-w-0">
              <Card>
                <CardHeader>
                  <CardTitle className="break-words">
                    {connection.name}
                  </CardTitle>
                  <p className="text-sm text-muted-foreground">
                    {connection.driver} ·{" "}
                    {connection.connected ? "Connected" : "Disconnected"}
                  </p>
                  <p className="break-all font-mono text-xs text-muted-foreground">
                    {connection.id}
                  </p>
                </CardHeader>
                <CardContent>
                  <Link
                    className="inline-flex h-8 items-center rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    href={`/migration/${encodeURIComponent(connection.id)}`}
                  >
                    Open workspace
                  </Link>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}

      <ConfirmDialog
        confirmLabel={
          pendingRecovery?.action === "resume"
            ? "Resume batch job"
            : "Revert committed batches"
        }
        description={
          pendingRecovery?.action === "resume"
            ? `Queue resume_batch for job ${pendingRecovery.job.id} starting after batch ${pendingRecovery.job.batchesCommitted ?? 0}?`
            : `Queue compensating revert_batch for job ${pendingRecovery?.job.id ?? ""} to undo committed batches in reverse order?`
        }
        destructive={pendingRecovery?.action === "revert"}
        onConfirm={() => void handleConfirmRecovery()}
        onOpenChange={(open) => {
          if (!open && !busyJobId) setPendingRecovery(undefined);
        }}
        open={Boolean(pendingRecovery)}
        pending={Boolean(busyJobId)}
        title={
          pendingRecovery?.action === "resume"
            ? "Resume batch migration"
            : "Revert committed batches"
        }
      />
    </div>
  );
}
