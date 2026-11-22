"use client";

import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Check, CircleX, Copy, Pencil, Play } from "lucide-react";
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

const dateTimeFormatter = new Intl.DateTimeFormat("en-US", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

function formatDateTime(value: string) {
  return dateTimeFormatter.format(new Date(value));
}

export type MigrationRecord = {
  id: string;
  sequence: number;
  name: string;
  type: "forward" | "rollback";
  status: string;
  reversible: boolean;
  executedBy: string;
  executedAt: string;
  durationMs?: number;
  cancelsMigrationId?: string;
};

export function MigrationHistory({
  records,
  hideRollbacks,
  onHideRollbacksChange,
}: {
  records: MigrationRecord[];
  hideRollbacks: boolean;
  onHideRollbacksChange: (hide: boolean) => void;
}) {
  const hiddenIds = new Set(
    hideRollbacks
      ? records
          .filter((record) => record.type === "rollback")
          .flatMap((record) => [
            record.id,
            ...(record.cancelsMigrationId ? [record.cancelsMigrationId] : []),
          ])
      : [],
  );
  const visibleRecords = records.filter((record) => !hiddenIds.has(record.id));
  const columns: DataTableColumn<MigrationRecord>[] = [
    { id: "sequence", header: "#", cell: (item) => item.sequence },
    { id: "name", header: "Migration", cell: (item) => item.name },
    { id: "type", header: "Type", cell: (item) => item.type },
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
    { id: "actor", header: "Executed by", cell: (item) => item.executedBy },
    {
      id: "executed",
      header: "Executed at",
      cell: (item) => formatDateTime(item.executedAt),
    },
    {
      id: "duration",
      header: "Duration",
      cell: (item) =>
        item.durationMs === undefined ? "—" : `${item.durationMs} ms`,
    },
  ];

  return (
    <section className="space-y-4">
      <label className="flex items-center gap-2 text-sm">
        <input
          checked={hideRollbacks}
          onChange={(event) => onHideRollbacksChange(event.target.checked)}
          type="checkbox"
        />
        Hide rollbacks and the forward migrations they cancel
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
  initialValue?: {
    [K in keyof DraftFormValues]: DraftFormValues[K];
  };
  busy?: boolean;
  onSave: (value: DraftFormValues) => void | Promise<void>;
}) {
  const [value, setValue] = useState({
    name: "",
    description: "",
    upSql: "",
    downSql: "",
    irreversible: false,
    mode: "single" as const,
    batchSize: 1000,
    cursorColumn: "",
    ...initialValue,
  });
  const update = <K extends keyof typeof value>(
    key: K,
    next: (typeof value)[K],
  ) => setValue((current) => ({ ...current, [key]: next }));

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
        Up SQL
        <Textarea
          className="min-h-40 resize-y font-mono"
          onChange={(event) => update("upSql", event.target.value)}
          required
          value={value.upSql}
        />
      </label>
      <label className="block space-y-2 text-sm font-medium">
        Down SQL
        <Textarea
          className="min-h-32 resize-y font-mono"
          disabled={value.irreversible}
          onChange={(event) => update("downSql", event.target.value)}
          required={!value.irreversible}
          value={value.downSql}
        />
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input
          checked={value.irreversible}
          onChange={(event) => update("irreversible", event.target.checked)}
          type="checkbox"
        />
        This migration is irreversible
      </label>
      <label className="block space-y-2 text-sm font-medium">
        Execution mode
        <select
          className="h-9 w-full rounded-md border bg-background px-3 text-sm"
          onChange={(event) =>
            update("mode", event.target.value as "single" | "batch")
          }
          value={value.mode}
        >
          <option value="single">Single transaction</option>
          <option value="batch">Batch</option>
        </select>
      </label>
      {value.mode === "batch" ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <NumberField
            label="Batch size"
            value={value.batchSize}
            onChange={(next) => update("batchSize", next)}
          />
          <Field
            label="Cursor column"
            value={value.cursorColumn}
            onChange={(next) => update("cursorColumn", next)}
            required
          />
        </div>
      ) : null}
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
  startedAt?: string;
  batchesCommitted?: number;
  batchesTotal?: number;
  rowsCommitted?: number;
  currentCursor?: string;
  lastError?: string;
};

const terminalStatuses = new Set([
  "succeeded",
  "reverted",
  "failed",
  "stale",
  "cancelled",
]);

export function JobProgress({
  initialJob,
  fetchJob,
}: {
  initialJob: JobSummary;
  fetchJob: (jobId: string) => Promise<JobSummary>;
}) {
  const [job, setJob] = useState(initialJob);
  const [error, setError] = useState<string>();
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inFlight = useRef(false);
  const stopped = useRef(false);
  const failures = useRef(0);

  useEffect(() => {
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

  return (
    <section
      aria-live="polite"
      className="space-y-4 rounded-lg border bg-background p-4"
    >
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="font-semibold">Job {job.id}</h2>
          {job.startedAt ? (
            <p className="text-sm text-muted-foreground">
              Started {formatDateTime(job.startedAt)}
            </p>
          ) : null}
        </div>
        <StatusBadge status={job.status} />
      </header>
      <dl className="grid gap-3 text-sm sm:grid-cols-2">
        <JobMetric label="Batches committed">
          {job.batchesCommitted ?? "—"}
          {job.batchesTotal === undefined ? "" : ` / ${job.batchesTotal}`}
        </JobMetric>
        <JobMetric label="Rows committed">{job.rowsCommitted ?? "—"}</JobMetric>
        <JobMetric label="Current cursor">{job.currentCursor ?? "—"}</JobMetric>
        <JobMetric label="Last error">{job.lastError ?? "—"}</JobMetric>
      </dl>
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
}: {
  jobs: JobSummary[];
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onSelect?: (job: JobSummary) => void;
}) {
  const columns: DataTableColumn<JobSummary>[] = [
    {
      id: "id",
      header: "Job",
      cell: (job) =>
        onSelect ? (
          <button
            className="font-mono text-primary underline"
            onClick={() => onSelect(job)}
            type="button"
          >
            {job.id}
          </button>
        ) : (
          <span className="font-mono">{job.id}</span>
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
  ];
  return (
    <div className="space-y-4">
      <DataTable columns={columns} getRowId={(job) => job.id} rows={jobs} />
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
