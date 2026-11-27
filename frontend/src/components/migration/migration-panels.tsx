"use client";

import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  Check,
  CircleX,
  Copy,
  Pencil,
  Play,
  RotateCcw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  DataTable,
  type DataTableColumn,
} from "@/components/shared/data-table";
import { ErrorState } from "@/components/shared/async-states";
import { PaginationControls } from "@/components/shared/pagination-controls";
import { StatusBadge } from "@/components/shared/status-badge";
import type {
  MigrationHistoryEntry,
  MigrationJob,
} from "@/lib/api/migrations";
import { visibleMigrationHistory } from "@/lib/migration-history";

const dateTimeFormatter = new Intl.DateTimeFormat("en-US", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

function formatDateTime(value: string) {
  return dateTimeFormatter.format(new Date(value));
}

export function MigrationHistory({
  records,
}: {
  records: MigrationHistoryEntry[];
}) {
  const [hideRollbacks, setHideRollbacks] = useState(false);
  const visibleRecords = visibleMigrationHistory(records, hideRollbacks);
  const columns: DataTableColumn<MigrationHistoryEntry>[] = [
    {
      id: "execution",
      header: "Execution",
      cell: (item) => (
        <div>
          <span className="font-mono">#{item.id}</span>
          <div className="text-xs text-muted-foreground">
            Attempt {item.attempt_number}
          </div>
        </div>
      ),
    },
    {
      id: "name",
      header: "Migration",
      cell: (item) => (
        <div>
          <span className="font-medium">{item.title}</span>
          <div className="text-xs text-muted-foreground">
            #{item.migration_id} ·{" "}
            {item.version !== undefined ? `v${item.version} · ` : ""}Revision{" "}
            {item.revision}
          </div>
        </div>
      ),
    },
    {
      id: "operation",
      header: "Operation",
      cell: (item) => (
        <div>
          {item.operation}
          {item.rollback_target_run_id && (
            <div className="text-xs text-muted-foreground">
              Target #{item.rollback_target_run_id}
            </div>
          )}
        </div>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (item) => <StatusBadge status={item.status} />,
    },
    {
      id: "reversible",
      header: "Rollback",
      cell: (item) => (item.reversible ? "Reversible" : "Irreversible"),
    },
    { id: "actor", header: "Executed by", cell: (item) => item.username },
    {
      id: "executed",
      header: "Started (UTC)",
      cell: (item) => formatDateTime(item.started_at),
    },
    {
      id: "duration",
      header: "Duration",
      cell: (item) =>
        item.finished_at === undefined ? "—" : `${item.execution_ms} ms`,
    },
    {
      id: "sql",
      header: "SQL snapshot",
      cell: (item) => (
        <div>
          <p className="text-xs text-muted-foreground">
            {item.sql_dispatch_started === true
              ? "Dispatch started"
              : item.sql_dispatch_started === false
                ? "Dispatch not started"
                : "Dispatch not recorded"}
          </p>
          <details>
            <summary className="cursor-pointer underline">
              {item.sql_dispatch_started === false
                ? "View planned SQL"
                : "View SQL snapshot"}
            </summary>
            <pre className="mt-2 max-h-52 w-64 max-w-[70vw] overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">
              {item.sql_snapshot}
            </pre>
          </details>
        </div>
      ),
    },
  ];

  return (
    <section className="space-y-4">
      <label className="flex items-center gap-2 text-sm">
        <input
          checked={hideRollbacks}
          onChange={(event) => setHideRollbacks(event.target.checked)}
          type="checkbox"
        />
        Hide rollback
      </label>
      <DataTable
        columns={columns}
        emptyTitle="No migration history"
        getRowId={(item) => item.id}
        rows={visibleRecords}
      />
    </section>
  );
}

export type DraftSummary = {
  id: string;
  name: string;
  status: "valid" | "invalid" | "stale" | "immutable" | string;
  mode: "single" | "batch";
  updatedAt?: string;
};

export type DraftFormValues = {
  name: string;
  description: string;
  upSql: string;
  downSql: string;
  irreversible: boolean;
  irreversibleReason?: string;
  mode: "single" | "batch";
  batchSize: number;
  cursorColumn: string;
};

export function DraftList({
  drafts,
  canEdit,
  onEdit,
  onDelete,
  onValidate,
  onRebase,
  onDuplicate,
  onRequest,
}: {
  drafts: DraftSummary[];
  canEdit: boolean;
  onEdit?: (draft: DraftSummary) => void;
  onDelete?: (draft: DraftSummary) => void;
  onValidate?: (draft: DraftSummary) => void;
  onRebase?: (draft: DraftSummary) => void;
  onDuplicate?: (draft: DraftSummary) => void;
  onRequest?: (draft: DraftSummary) => void;
}) {
  const columns: DataTableColumn<DraftSummary>[] = [
    { id: "name", header: "Draft", cell: (draft) => draft.name },
    { id: "mode", header: "Mode", cell: (draft) => draft.mode },
    {
      id: "status",
      header: "Validation",
      cell: (draft) => <StatusBadge status={draft.status} />,
    },
    {
      id: "actions",
      header: "Actions",
      cell: (draft) => (
        <div className="flex flex-wrap gap-1">
          {canEdit && onEdit && draft.status !== "immutable" ? (
            <Button onClick={() => onEdit(draft)} size="sm" variant="outline">
              <Pencil aria-hidden="true" />
              Edit
            </Button>
          ) : null}
          {canEdit && onValidate ? (
            <Button
              onClick={() => onValidate(draft)}
              size="sm"
              variant="outline"
            >
              Validate
            </Button>
          ) : null}
          {canEdit && onRebase && draft.status === "stale" ? (
            <Button onClick={() => onRebase(draft)} size="sm" variant="outline">
              Rebase
            </Button>
          ) : null}
          {canEdit && onRequest && draft.status === "valid" ? (
            <Button onClick={() => onRequest(draft)} size="sm">
              <Play aria-hidden="true" />
              Request
            </Button>
          ) : null}
          {onDuplicate ? (
            <Button
              onClick={() => onDuplicate(draft)}
              size="sm"
              variant="outline"
            >
              <Copy aria-hidden="true" />
              Duplicate
            </Button>
          ) : null}
          {canEdit && onDelete && draft.status !== "immutable" ? (
            <Button
              aria-label={`Delete ${draft.name}`}
              onClick={() => onDelete(draft)}
              size="icon-sm"
              variant="destructive"
            >
              <CircleX aria-hidden="true" />
            </Button>
          ) : null}
        </div>
      ),
    },
  ];
  return (
    <DataTable columns={columns} getRowId={(draft) => draft.id} rows={drafts} />
  );
}

export function DraftEditor({
  initialValue,
  busy = false,
  onSave,
}: {
  initialValue?: Partial<DraftFormValues>;
  busy?: boolean;
  onSave: (value: DraftFormValues) => void | Promise<void>;
}) {
  const [value, setValue] = useState<DraftFormValues>({
    name: "",
    description: "",
    upSql: "",
    downSql: "",
    irreversible: false,
    irreversibleReason: "",
    mode: "single",
    batchSize: 1000,
    cursorColumn: "id",
    ...initialValue,
  });
  const update = <K extends keyof DraftFormValues>(
    key: K,
    next: DraftFormValues[K],
  ) => setValue((current) => ({ ...current, [key]: next }));

  const cursorName = value.cursorColumn.trim() || "id";

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault();
        void onSave(value);
      }}
    >
      <Field
        label="Name"
        value={value.name}
        onChange={(next) => update("name", next)}
        required
      />
      <Field
        label="Description"
        value={value.description}
        onChange={(next) => update("description", next)}
      />
      <label className="block space-y-2 text-sm font-medium">
        Execution mode
        <select
          className="h-9 w-full rounded-md border bg-background px-3 text-sm"
          onChange={(event) =>
            update("mode", event.target.value as "single" | "batch")
          }
          value={value.mode}
        >
          <option value="single">Normal (single transaction)</option>
          <option value="batch">Batch (cursor chunked with checkpoints)</option>
        </select>
      </label>
      {value.mode === "batch" ? (
        <div className="space-y-3 rounded-lg border bg-muted/30 p-3">
          <div className="grid gap-4 sm:grid-cols-2">
            <NumberField
              label="Batch size (rows per transaction)"
              value={value.batchSize}
              onChange={(next) => update("batchSize", next)}
            />
            <Field
              label="Cursor attribute (batch_by_attribute)"
              value={value.cursorColumn}
              onChange={(next) => update("cursorColumn", next)}
              required
            />
          </div>
          <div className="space-y-1 text-xs text-muted-foreground">
            <p className="font-medium text-foreground">
              Batch SQL parameter contract
            </p>
            <p>
              Use <code className="font-mono">$1</code> (last committed cursor),{" "}
              <code className="font-mono">$2</code> (target scope max), and{" "}
              <code className="font-mono">$3</code> (batch size) in Up SQL, and{" "}
              <code className="font-mono">$1</code> (cursor start) /{" "}
              <code className="font-mono">$2</code> (cursor end) in Down SQL.
            </p>
            <pre className="overflow-auto rounded bg-muted p-2 font-mono text-[11px] text-foreground">
              {`WITH batch AS (\n  SELECT ${cursorName} FROM target_table\n  WHERE ${cursorName} > $1 AND ${cursorName} <= $2\n  ORDER BY ${cursorName} LIMIT $3\n)\nUPDATE target_table SET status = 'migrated'\nWHERE ${cursorName} IN (SELECT ${cursorName} FROM batch);`}
            </pre>
          </div>
        </div>
      ) : null}
      <label className="block space-y-2 text-sm font-medium">
        Up SQL
        <Textarea
          className="min-h-40 resize-y font-mono"
          onChange={(event) => update("upSql", event.target.value)}
          required
          value={value.upSql}
        />
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input
          checked={value.irreversible}
          onChange={(event) => {
            const checked = event.target.checked;
            setValue((current) => ({
              ...current,
              irreversible: checked,
              downSql: checked ? "" : current.downSql,
              irreversibleReason: checked ? current.irreversibleReason : "",
            }));
          }}
          type="checkbox"
        />
        This migration is irreversible
      </label>
      {value.irreversible ? (
        <Field
          label="Irreversible reason"
          value={value.irreversibleReason ?? ""}
          onChange={(next) => update("irreversibleReason", next)}
          required
        />
      ) : (
        <label className="block space-y-2 text-sm font-medium">
          Down SQL
          <Textarea
            className="min-h-32 resize-y font-mono"
            onChange={(event) => update("downSql", event.target.value)}
            required
            value={value.downSql}
          />
        </label>
      )}
      <Button disabled={busy} type="submit">
        {busy ? "Saving…" : "Save draft"}
      </Button>
    </form>
  );
}

export type ApprovalProgress = {
  groupId: string;
  groupName: string;
  leaderName: string;
  approved: boolean;
};

export type RequestSummary = {
  id: string;
  type: string;
  status: string;
  requester: string;
  createdAt: string;
};

export function RequestList({
  requests,
  onSelect,
}: {
  requests: RequestSummary[];
  onSelect?: (request: RequestSummary) => void;
}) {
  const columns: DataTableColumn<RequestSummary>[] = [
    { id: "type", header: "Request", cell: (request) => request.type },
    {
      id: "status",
      header: "Status",
      cell: (request) => <StatusBadge status={request.status} />,
    },
    {
      id: "requester",
      header: "Requested by",
      cell: (request) => request.requester,
    },
    {
      id: "created",
      header: "Created",
      cell: (request) => formatDateTime(request.createdAt),
    },
    {
      id: "detail",
      header: "Details",
      cell: (request) =>
        onSelect ? (
          <Button onClick={() => onSelect(request)} size="sm" variant="outline">
            Review
          </Button>
        ) : null,
    },
  ];
  return (
    <DataTable
      columns={columns}
      getRowId={(request) => request.id}
      rows={requests}
    />
  );
}

export function RequestReview({
  approvals,
  sqlPreview,
  rejectCommentRequired = true,
  busy = false,
  onApprove,
  onReject,
}: {
  approvals: ApprovalProgress[];
  sqlPreview: string;
  rejectCommentRequired?: boolean;
  busy?: boolean;
  onApprove: () => void;
  onReject: (comment: string) => void;
}) {
  const [comment, setComment] = useState("");
  return (
    <section className="space-y-5">
      <div>
        <h2 className="font-semibold">SQL preview</h2>
        <pre className="mt-2 max-h-96 overflow-auto rounded-lg border bg-muted/40 p-4 text-xs">
          <code>{sqlPreview}</code>
        </pre>
      </div>
      <div>
        <h2 className="font-semibold">Approval progress</h2>
        <ul className="mt-2 space-y-2">
          {approvals.map((approval) => (
            <li
              className="flex flex-wrap items-center justify-between gap-2 rounded-md border p-3 text-sm"
              key={approval.groupId}
            >
              <span>
                {approval.groupName} · {approval.leaderName}
              </span>
              <StatusBadge
                status={approval.approved ? "approved" : "pending"}
              />
            </li>
          ))}
        </ul>
      </div>
      <div className="space-y-2">
        <Label htmlFor="reject-comment">Review comment</Label>
        <Textarea
          id="reject-comment"
          onChange={(event) => setComment(event.target.value)}
          placeholder="A comment is required when rejecting."
          value={comment}
        />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy} onClick={onApprove}>
          <Check aria-hidden="true" />
          Approve
        </Button>
        <Button
          disabled={busy || (rejectCommentRequired && !comment.trim())}
          onClick={() => onReject(comment.trim())}
          variant="destructive"
        >
          <CircleX aria-hidden="true" />
          Reject
        </Button>
      </div>
    </section>
  );
}

export type JobSummary = {
  id: string;
  status: string;
  action?: string;
  mode?: string;
  migrationId?: number;
  executionId?: string;
  actorUsername?: string;
  enqueuedAt?: string;
  startedAt?: string;
  finishedAt?: string;
  batchesCommitted?: number;
  batchesTotal?: number;
  rowsCommitted?: number;
  currentCursor?: string;
  targetScopeMax?: string;
  attempt?: number;
  maxRetries?: number;
  sqlState?: string;
  lastError?: string;
};

export function toJobSummary(job: MigrationJob): JobSummary {
  return {
    id: job.job_id,
    status: job.status,
    action: job.action,
    mode: job.mode,
    migrationId: job.migration_id,
    executionId: job.execution_id,
    actorUsername: job.actor_username,
    enqueuedAt: job.enqueued_at,
    startedAt: job.started_at ?? undefined,
    finishedAt: job.finished_at ?? undefined,
    batchesCommitted: job.last_committed_batch,
    rowsCommitted: job.committed_rows,
    currentCursor: job.last_committed_cursor ?? undefined,
    targetScopeMax: job.target_scope_max ?? undefined,
    attempt: job.attempt,
    maxRetries: job.max_retries,
    sqlState: job.sqlstate ?? undefined,
    lastError: job.error_message ?? undefined,
  };
}

const terminalStatuses = new Set([
  "success",
  "succeeded",
  "reverted",
  "failed",
  "partial",
  "partially_reverted",
  "stale",
  "cancelled",
]);

export function canRecoverPartialJob(status: string): boolean {
  return status === "partial" || status === "partially_reverted";
}

export function JobProgress({
  initialJob,
  fetchJob,
  onTerminal,
  onResume,
  onRevert,
  actionBusy = false,
}: {
  initialJob: JobSummary;
  fetchJob: (jobId: string) => Promise<JobSummary>;
  onTerminal?: (job: JobSummary) => void;
  onResume?: (job: JobSummary) => void;
  onRevert?: (job: JobSummary) => void;
  actionBusy?: boolean;
}) {
  const [job, setJob] = useState(initialJob);
  const [error, setError] = useState<string>();
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inFlight = useRef(false);
  const stopped = useRef(false);
  const failures = useRef(0);
  const onTerminalRef = useRef(onTerminal);

  useEffect(() => {
    onTerminalRef.current = onTerminal;
  }, [onTerminal]);

  useEffect(() => {
    failures.current = 0;
    stopped.current = terminalStatuses.has(initialJob.status);
    let disposed = false;

    const clearTimer = () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
    };

    const schedule = (delay: number) => {
      clearTimer();
      if (!disposed && !stopped.current && !document.hidden) {
        timer.current = setTimeout(() => void poll(), delay);
      }
    };

    const poll = async () => {
      if (disposed || stopped.current || document.hidden || inFlight.current)
        return;
      inFlight.current = true;
      try {
        const nextJob = await fetchJob(initialJob.id);
        if (disposed) return;
        setJob(nextJob);
        setError(undefined);
        failures.current = 0;
        if (terminalStatuses.has(nextJob.status)) {
          stopped.current = true;
          clearTimer();
          onTerminalRef.current?.(nextJob);
          return;
        }
        schedule(2000);
      } catch (cause) {
        if (disposed) return;
        setError(
          cause instanceof Error
            ? cause.message
            : "Unable to refresh job status.",
        );
        failures.current += 1;
        schedule(Math.min(2000 * 2 ** failures.current, 10000));
      } finally {
        inFlight.current = false;
      }
    };

    const onVisibilityChange = () => {
      if (document.hidden) clearTimer();
      else if (!stopped.current) void poll();
    };

    document.addEventListener("visibilitychange", onVisibilityChange);
    if (!stopped.current) void poll();
    return () => {
      disposed = true;
      clearTimer();
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [fetchJob, initialJob.id, initialJob.status]);

  const recoverable = canRecoverPartialJob(job.status);

  return (
    <section
      aria-live="polite"
      className="space-y-4 rounded-lg border bg-background p-4"
    >
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <h2 className="break-all font-mono text-sm font-semibold">
            Job {job.id}
          </h2>
          <p className="text-xs text-muted-foreground">
            {job.action ? `${job.action.replaceAll("_", " ")} · ` : ""}
            {job.mode ? `${job.mode} mode · ` : ""}
            {job.migrationId !== undefined ? `Migration #${job.migrationId}` : ""}
            {job.actorUsername ? ` · by ${job.actorUsername}` : ""}
          </p>
          {job.executionId ? (
            <p className="break-all font-mono text-xs text-muted-foreground">
              Execution {job.executionId}
            </p>
          ) : null}
          {job.startedAt ? (
            <p className="text-xs text-muted-foreground">
              Started {formatDateTime(job.startedAt)}
              {job.finishedAt ? ` · Finished ${formatDateTime(job.finishedAt)}` : ""}
            </p>
          ) : job.enqueuedAt ? (
            <p className="text-xs text-muted-foreground">
              Enqueued {formatDateTime(job.enqueuedAt)}
            </p>
          ) : null}
        </div>
        <StatusBadge status={job.status} />
      </header>
      <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <JobMetric label="Batches committed">
          {job.batchesCommitted ?? "—"}
          {job.batchesTotal === undefined ? "" : ` / ${job.batchesTotal}`}
        </JobMetric>
        <JobMetric label="Rows committed">{job.rowsCommitted ?? "—"}</JobMetric>
        <JobMetric label="Current cursor">
          {job.currentCursor ?? "—"}
          {job.targetScopeMax ? ` / ${job.targetScopeMax}` : ""}
        </JobMetric>
        <JobMetric label="Attempts">
          {job.attempt !== undefined
            ? `${job.attempt}${job.maxRetries !== undefined ? ` / ${job.maxRetries}` : ""}`
            : "—"}
        </JobMetric>
      </dl>
      {job.lastError ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm">
          <p className="font-medium text-destructive">
            Last error{job.sqlState ? ` (SQLSTATE ${job.sqlState})` : ""}
          </p>
          <p className="mt-1 wrap-break-word text-xs">{job.lastError}</p>
        </div>
      ) : null}
      {recoverable && (onResume || onRevert) ? (
        <div className="space-y-3 rounded-md border border-destructive/30 bg-muted/40 p-3">
          <p className="text-xs text-muted-foreground">
            Partial batch state is holding the database lock. Choose whether to
            resume from the last committed checkpoint or revert committed
            batches in reverse order.
          </p>
          <div className="flex flex-wrap gap-2">
            {onResume ? (
              <Button
                disabled={actionBusy}
                onClick={() => onResume(job)}
                size="sm"
              >
                <Play aria-hidden="true" />
                Resume from checkpoint
              </Button>
            ) : null}
            {onRevert ? (
              <Button
                disabled={actionBusy}
                onClick={() => onRevert(job)}
                size="sm"
                variant="destructive"
              >
                <RotateCcw aria-hidden="true" />
                Revert committed batches
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
      {error ? (
        <ErrorState
          message={error}
          onRetry={() => {
            failures.current = 0;
            void fetchJob(job.id)
              .then((nextJob) => {
                setJob(nextJob);
                setError(undefined);
              })
              .catch((cause: unknown) =>
                setError(
                  cause instanceof Error
                    ? cause.message
                    : "Unable to refresh job status.",
                ),
              );
          }}
          retryLabel="Refresh now"
          title="Could not refresh job progress"
        />
      ) : null}
    </section>
  );
}

export function JobList({
  jobs,
  page,
  pageSize,
  total,
  onPageChange,
  onSelect,
  onResume,
  onRevert,
  actionBusyJobId,
}: {
  jobs: JobSummary[];
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onSelect?: (job: JobSummary) => void;
  onResume?: (job: JobSummary) => void;
  onRevert?: (job: JobSummary) => void;
  actionBusyJobId?: string;
}) {
  const columns: DataTableColumn<JobSummary>[] = [
    {
      id: "id",
      header: "Job",
      cell: (job) => (
        <div className="min-w-0">
          {onSelect ? (
            <button
              className="break-all text-left font-mono text-xs text-primary underline"
              onClick={() => onSelect(job)}
              type="button"
            >
              {job.id}
            </button>
          ) : (
            <span className="break-all font-mono text-xs">{job.id}</span>
          )}
          {job.migrationId !== undefined ? (
            <div className="text-xs text-muted-foreground">
              Migration #{job.migrationId}
              {job.actorUsername ? ` · ${job.actorUsername}` : ""}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      id: "action",
      header: "Operation",
      cell: (job) => (
        <div className="text-xs">
          <div className="font-medium">
            {job.action ? job.action.replaceAll("_", " ") : "—"}
          </div>
          {job.mode ? (
            <div className="text-muted-foreground">{job.mode}</div>
          ) : null}
        </div>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (job) => <StatusBadge status={job.status} />,
    },
    {
      id: "batches",
      header: "Batches",
      cell: (job) => job.batchesCommitted ?? "—",
    },
    { id: "rows", header: "Rows", cell: (job) => job.rowsCommitted ?? "—" },
    {
      id: "actions",
      header: "Recovery",
      cell: (job) => {
        if (!canRecoverPartialJob(job.status) || (!onResume && !onRevert)) {
          return <span className="text-xs text-muted-foreground">—</span>;
        }
        const busy = actionBusyJobId === job.id;
        return (
          <div className="flex flex-wrap gap-1">
            {onResume ? (
              <Button
                disabled={busy}
                onClick={() => onResume(job)}
                size="sm"
                variant="outline"
              >
                <Play aria-hidden="true" />
                Resume
              </Button>
            ) : null}
            {onRevert ? (
              <Button
                disabled={busy}
                onClick={() => onRevert(job)}
                size="sm"
                variant="destructive"
              >
                <RotateCcw aria-hidden="true" />
                Revert
              </Button>
            ) : null}
          </div>
        );
      },
    },
  ];
  return (
    <div className="space-y-4">
      <DataTable
        columns={columns}
        emptyTitle="No migration jobs"
        getRowId={(job) => job.id}
        rows={jobs}
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

function JobMetric({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="rounded-md bg-muted/50 p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 wrap-break-word font-medium">{children}</dd>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  required = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  required?: boolean;
}) {
  return (
    <label className="block space-y-2 text-sm font-medium">
      {label}
      <Input
        onChange={(event) => onChange(event.target.value)}
        required={required}
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
    <label className="block space-y-2 text-sm font-medium">
      {label}
      <Input
        min={1}
        onChange={(event) => onChange(Number(event.target.value))}
        required
        type="number"
        value={value}
      />
    </label>
  );
}
