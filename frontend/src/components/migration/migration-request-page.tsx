"use client";

import { useEffect, useState } from "react";
import { RequestReview } from "@/components/migration/migration-panels";
import { ErrorState, LoadingState } from "@/components/shared/async-states";
import { PageHeader } from "@/components/shared/page-header";
import { StatusBadge } from "@/components/shared/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  approveMigrationRequest,
  enqueueMigrationRequest,
  getMigrationRequest,
  type MigrationRequest,
} from "@/lib/api/migrations";

export function MigrationRequestPage({ connectionId, requestId }: { connectionId: string; requestId: number }) {
  const [request, setRequest] = useState<MigrationRequest>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copyStatus, setCopyStatus] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    getMigrationRequest(connectionId, requestId, signal).then(
      (result) => { if (!signal.aborted) setRequest(result); },
      (error: unknown) => { if (!signal.aborted) setError(requestError(error)); },
    );
    return () => controller.abort();
  }, [connectionId, requestId]);

  async function updateRequest(action: () => Promise<MigrationRequest>) {
    setBusy(true);
    setError("");
    try {
      setRequest(await action());
    } catch (error) {
      setError(requestError(error));
    } finally {
      setBusy(false);
    }
  }

  function refresh() {
    void updateRequest(() => getMigrationRequest(connectionId, requestId));
  }

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(window.location.href);
      setCopyStatus("Review link copied.");
    } catch {
      setCopyStatus("Copy the URL from your browser’s address bar to share this request.");
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        breadcrumbs={[
          { label: "Migrations", href: "/migration" },
          { label: "Database migrations", href: `/migration/${encodeURIComponent(connectionId)}` },
          { label: `Request #${requestId}` },
        ]}
        title={`Migration request #${requestId}`}
        actions={request && (
          <>
            <Button onClick={() => void copyLink()} variant="outline">Copy review link</Button>
            <Button disabled={busy} onClick={refresh} variant="outline">Refresh</Button>
          </>
        )}
      />
      {!request ? (
        error ? <ErrorState message={error} onRetry={refresh} /> : <LoadingState label="Loading migration request" />
      ) : (
        <>
          {copyStatus && <p className="text-sm text-muted-foreground" role="status">{copyStatus}</p>}
          {error && <p className="text-sm text-destructive" role="alert">{error}</p>}
          <Card>
            <CardContent className="space-y-5">
              <div className="flex flex-wrap items-center gap-3">
                <StatusBadge status={request.status} />
                <p className="text-sm">
                  {request.operation} · Migration #{request.migration_id} · Revision {request.revision}
                  {request.target_run_id !== undefined && ` · Target execution #${request.target_run_id}`}
                </p>
              </div>
              <RequestReview
                busy={busy}
                onApprove={(groupId) => void updateRequest(() => approveMigrationRequest(connectionId, requestId, groupId))}
                request={request}
              />
              {request.status === "APPROVED" && request.can_execute && (
                <Button disabled={busy} onClick={() => void updateRequest(async () => {
                  const { job_id } = await enqueueMigrationRequest(connectionId, requestId);
                  return { ...request, status: "QUEUED", job_id, can_execute: false };
                })}>
                  Execute
                </Button>
              )}
              {request.job_id && <p className="break-all text-sm" role="status">Job ID <code>{request.job_id}</code></p>}
              {request.status === "STALE" && <p className="text-sm">The revision or target changed. Submit a new request for review.</p>}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}

function requestError(error: unknown) {
  return error instanceof Error && error.name !== "ZodError" ? error.message : "Could not load or update the migration request.";
}
