"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { MigrationActions } from "@/components/migration/migration-actions";
import {
  JobList,
  JobProgress,
  MigrationHistory,
  toJobSummary,
  type JobSummary,
} from "@/components/migration/migration-panels";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/components/shared/async-states";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import { PageHeader } from "@/components/shared/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  enqueueJobRecovery,
  getMigrationJob,
  listMigrationConnections,
  listMigrationJobs,
  loadMigrationWorkspace,
  type MigrationConnection,
  type MigrationJob,
} from "@/lib/api/migrations";

type WorkspaceTab = "migrations" | "jobs" | "history";

type WorkspaceData = Awaited<ReturnType<typeof loadMigrationWorkspace>> & {
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
  const [busyJobId, setBusyJobId] = useState<string>();
  const [selectedJob, setSelectedJob] = useState<JobSummary>();
  const [pendingRecovery, setPendingRecovery] = useState<PendingRecovery>();
  const [jobsPage, setJobsPage] = useState(1);

  const loadFullWorkspace = useCallback(
    async (id: string, signal: AbortSignal): Promise<WorkspaceData> => {
      const [workspace, jobs] = await Promise.all([
        loadMigrationWorkspace(id, signal),
        listMigrationJobs(id, signal, { limit: 100, offset: 0 }).catch(
          () => [] as MigrationJob[],
        ),
      ]);
      return { ...workspace, jobs };
    },
    [],
  );

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    const request: Promise<PageData> = connectionId
      ? loadFullWorkspace(connectionId, signal)
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
  }, [connectionId, loadFullWorkspace, retry]);

  const refreshWorkspace = useCallback(async () => {
    if (!connectionId) {
      setRetry((attempt) => attempt + 1);
      return;
    }
    const controller = new AbortController();
    try {
      const fresh = await loadFullWorkspace(connectionId, controller.signal);
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
  }, [connectionId, loadFullWorkspace]);

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
        status: enqueued.status ?? "pending",
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

  const workspaceHeader = data && "connection" in data ? data.connection : undefined;

  return (
    <div className="space-y-5">
      <PageHeader
        actions={
          workspaceHeader ? (
            <Button
              onClick={() => void refreshWorkspace()}
              size="sm"
              variant="outline"
            >
              <RefreshCw aria-hidden="true" />
              Refresh
            </Button>
          ) : undefined
        }
        breadcrumbs={
          connectionId ? [{ label: "Migrations", href: "/migration" }] : undefined
        }
        description={
          workspaceHeader
            ? `${workspaceHeader.driver} · ${workspaceHeader.connected ? "Connected" : "Disconnected"}`
            : undefined
        }
        title={
          workspaceHeader
            ? `Migrations · ${workspaceHeader.name}`
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
                ["migrations", `Migrations (${data.migrations.length})`],
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
            <MigrationActions
              canSubmit={data.canSubmit}
              connectionId={data.connection.id}
              migrations={data.migrations}
              onDraftCreated={(draft) =>
                setData((current) =>
                  current && "migrations" in current
                    ? { ...current, migrations: [draft, ...current.migrations] }
                    : current,
                )
              }
              runs={data.runs}
            />
          ) : null}

          {activeTab === "jobs" ? (
            <div className="space-y-5">
              {selectedJob ? (
                <JobProgress
                  actionBusy={busyJobId === selectedJob.id}
                  fetchJob={fetchJobSummary}
                  initialJob={selectedJob}
                  key={selectedJob.id}
                  onResume={
                    data.canSubmit
                      ? (job) => setPendingRecovery({ job, action: "resume" })
                      : undefined
                  }
                  onRevert={
                    data.canSubmit
                      ? (job) => setPendingRecovery({ job, action: "revert" })
                      : undefined
                  }
                  onTerminal={() => void refreshWorkspace()}
                />
              ) : null}
              <JobList
                actionBusyJobId={busyJobId}
                jobs={pagedJobs}
                onPageChange={setJobsPage}
                onResume={
                  data.canSubmit
                    ? (job) => setPendingRecovery({ job, action: "resume" })
                    : undefined
                }
                onRevert={
                  data.canSubmit
                    ? (job) => setPendingRecovery({ job, action: "revert" })
                    : undefined
                }
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
                    Open migrations
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
