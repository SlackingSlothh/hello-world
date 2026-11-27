"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import {
  JobProgress,
  RequestReview,
  toJobSummary,
  type JobSummary,
} from "@/components/migration/migration-panels";
import { ErrorState, LoadingState } from "@/components/shared/async-states";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import { PageHeader } from "@/components/shared/page-header";
import { StatusBadge } from "@/components/shared/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  approveMigrationRequest,
  enqueueJobRecovery,
  enqueueMigrationRequest,
  getMigrationJob,
  getMigrationRequest,
  type MigrationRequest,
} from "@/lib/api/migrations";

type PendingRecovery = {
  job: JobSummary;
  action: "resume" | "revert";
};

export function MigrationRequestPage({
  connectionId,
  requestId,
}: {
  connectionId: string;
  requestId: number;
}) {
  const [request, setRequest] = useState<MigrationRequest>();
  const [activeJobId, setActiveJobId] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [recoveryBusy, setRecoveryBusy] = useState(false);
  const [pendingRecovery, setPendingRecovery] = useState<PendingRecovery>();
  const [error, setError] = useState("");
  const [copyStatus, setCopyStatus] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    getMigrationRequest(connectionId, requestId, signal).then(
      (result) => {
        if (signal.aborted) return;
        setRequest(result);
        if (result.job_id) setActiveJobId(result.job_id);
      },
      (cause: unknown) => {
        if (!signal.aborted) setError(requestError(cause));
      },
    );
    return () => controller.abort();
  }, [connectionId, requestId]);

  async function updateRequest(action: () => Promise<MigrationRequest>) {
    setBusy(true);
    setError("");
    try {
      const next = await action();
      setRequest(next);
      if (next.job_id) setActiveJobId(next.job_id);
    } catch (cause) {
      setError(requestError(cause));
    } finally {
      setBusy(false);
    }
  }

  function refresh() {
    void updateRequest(() => getMigrationRequest(connectionId, requestId));
  }

  const fetchJobSummary = useCallback(async (jobId: string) => {
    return toJobSummary(await getMigrationJob(jobId));
  }, []);

  async function handleConfirmRecovery() {
    if (!pendingRecovery) return;
    setRecoveryBusy(true);
    setError("");
    try {
      const enqueued = await enqueueJobRecovery(
        connectionId,
        pendingRecovery.job.id,
        pendingRecovery.action,
      );
      setActiveJobId(enqueued.job_id);
      setPendingRecovery(undefined);
      toast.success(
        pendingRecovery.action === "resume"
          ? `Resume job ${enqueued.job_id} queued.`
          : `Revert job ${enqueued.job_id} queued.`,
      );
    } catch (cause) {
      const msg = requestError(cause);
      setError(msg);
      toast.error(msg);
    } finally {
      setRecoveryBusy(false);
    }
  }

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(window.location.href);
      setCopyStatus("Review link copied.");
    } catch {
      setCopyStatus(
        "Copy the URL from your browser’s address bar to share this request.",
      );
    }
  }

  const trackedJobId = activeJobId ?? request?.job_id;

  return (
    <div className="space-y-5">
      <PageHeader
        breadcrumbs={[
          { label: "Migrations", href: "/migration" },
          {
            label: "Database migrations",
            href: `/migration/${encodeURIComponent(connectionId)}`,
          },
          { label: `Request #${requestId}` },
        ]}
        title={`Migration request #${requestId}`}
        actions={
          request && (
            <>
              <Button onClick={() => void copyLink()} variant="outline">
                Copy review link
              </Button>
              <Button disabled={busy} onClick={refresh} variant="outline">
                Refresh
              </Button>
            </>
          )
        }
      />
      {!request ? (
        error ? (
          <ErrorState message={error} onRetry={refresh} />
        ) : (
          <LoadingState label="Loading migration request" />
        )
      ) : (
        <>
          {copyStatus && (
            <p className="text-sm text-muted-foreground" role="status">
              {copyStatus}
            </p>
          )}
          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          <Card>
            <CardContent className="space-y-5">
              <div className="flex flex-wrap items-center gap-3">
                <StatusBadge status={request.status} />
                <p className="text-sm">
                  {request.operation} · Migration #{request.migration_id} ·
                  Revision {request.revision}
                  {request.target_run_id !== undefined &&
                    ` · Target execution #${request.target_run_id}`}
                </p>
              </div>
              <RequestReview
                busy={busy}
                onApprove={(groupId) =>
                  void updateRequest(() =>
                    approveMigrationRequest(connectionId, requestId, groupId),
                  )
                }
                request={request}
              />
              {request.status === "APPROVED" && request.can_execute && (
                <Button
                  disabled={busy}
                  onClick={() =>
                    void updateRequest(async () => {
                      const { job_id } = await enqueueMigrationRequest(
                        connectionId,
                        requestId,
                      );
                      setActiveJobId(job_id);
                      return {
                        ...request,
                        status: "QUEUED",
                        job_id,
                        can_execute: false,
                      };
                    })
                  }
                >
                  Execute
                </Button>
              )}
              {trackedJobId && (
                <div className="space-y-3">
                  <p className="break-all text-sm" role="status">
                    Job ID <code>{trackedJobId}</code> ·{" "}
                    <Link
                      className="text-primary underline"
                      href={`/migration/${encodeURIComponent(connectionId)}`}
                    >
                      View all database jobs
                    </Link>
                  </p>
                  <JobProgress
                    actionBusy={recoveryBusy}
                    fetchJob={fetchJobSummary}
                    initialJob={{
                      id: trackedJobId,
                      migrationId: request.migration_id,
                      status: "pending",
                    }}
                    key={trackedJobId}
                    onResume={(job) =>
                      setPendingRecovery({ job, action: "resume" })
                    }
                    onRevert={(job) =>
                      setPendingRecovery({ job, action: "revert" })
                    }
                  />
                </div>
              )}
              {request.status === "STALE" && (
                <p className="text-sm">
                  The revision or target changed. Submit a new request for
                  review.
                </p>
              )}
            </CardContent>
          </Card>
        </>
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
          if (!open && !recoveryBusy) setPendingRecovery(undefined);
        }}
        open={Boolean(pendingRecovery)}
        pending={recoveryBusy}
        title={
          pendingRecovery?.action === "resume"
            ? "Resume batch migration"
            : "Revert committed batches"
        }
      />
    </div>
  );
}

function requestError(error: unknown) {
  return error instanceof Error && error.name !== "ZodError"
    ? error.message
    : "Could not load or update the migration request.";
}
