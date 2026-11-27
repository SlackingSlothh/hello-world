import { z } from "zod";

const connectionSchema = z.object({
  id: z.string(),
  name: z.string(),
  driver: z.string(),
  connected: z.boolean(),
});

const historyEntrySchema = z.object({
  id: z.string(),
  migration_id: z.string(),
  title: z.string(),
  reversible: z.boolean(),
  version: z.number().optional(),
  attempt_number: z.number(),
  revision: z.number(),
  operation: z.enum([
    "APPLY",
    "ROLLBACK",
    "FORWARD",
    "RESUME_BATCH",
    "REVERT_BATCH",
  ]),
  rollback_target_run_id: z.string().optional(),
  status: z.enum(["RUNNING", "SUCCEEDED", "FAILED"]),
  sql_snapshot: z.string(),
  sql_dispatch_started: z.boolean().nullish(),
  executed_by: z.string(),
  username: z.string(),
  started_at: z.iso.datetime({ offset: true }),
  finished_at: z.iso.datetime({ offset: true }).optional(),
  execution_ms: z.number(),
});

const historySchema = z.object({
  database_id: z.string(),
  runs: z
    .array(historyEntrySchema)
    .nullable()
    .transform((value) => value ?? []),
});

const executionModeSchema = z.enum(["normal", "batch"]);

const migrationDefinitionSchema = z.object({
  id: z.number().int().positive(),
  database_id: z.string().optional(),
  version: z.number().int().positive().nullish(),
  title: z.string(),
  description: z.string().optional(),
  sql_content: z.string().optional(),
  sql_down: z.string().optional(),
  irreversible_reason: z.string().optional(),
  execution_mode: executionModeSchema.optional(),
  batch_by_attribute: z.string().optional(),
  batch_size: z.number().int().positive().nullish(),
  revision: z.number().int().positive(),
  frozen_at: z.iso.datetime({ offset: true }).optional(),
  status: z.enum(["DRAFT", "FAILED", "APPLIED"]),
  created_by: z.string().optional(),
  updated_by: z.string().optional(),
  created_at: z.iso.datetime({ offset: true }).optional(),
  updated_at: z.iso.datetime({ offset: true }).optional(),
  applied_at: z.iso.datetime({ offset: true }).optional(),
});

const definitionsSchema = z.object({
  database_id: z.string(),
  can_submit: z.boolean().optional().default(false),
  migrations: z
    .array(migrationDefinitionSchema)
    .nullable()
    .transform((migrations) => migrations ?? []),
});

const jobActionSchema = z.enum([
  "execute",
  "rollback",
  "forward",
  "resume_batch",
  "revert_batch",
]);

const jobStatusSchema = z.enum([
  "pending",
  "running",
  "success",
  "failed",
  "partial",
  "partially_reverted",
  "uncertain",
]);

const migrationJobSchema = z.object({
  job_id: z.string(),
  migration_id: z.number(),
  execution_id: z.string(),
  parent_execution_id: z.string().nullish(),
  database_id: z.string(),
  actor_id: z.string(),
  actor_username: z.string(),
  action: jobActionSchema,
  mode: executionModeSchema,
  status: jobStatusSchema,
  batch_by_attribute: z.string().nullish(),
  batch_size: z.number().nullish(),
  target_scope_max: z.string().nullish(),
  last_committed_batch: z.number(),
  last_committed_cursor: z.string().nullish(),
  committed_rows: z.number(),
  attempt: z.number(),
  max_retries: z.number(),
  next_retry_at: z.iso.datetime({ offset: true }).nullish(),
  error_message: z.string().nullish(),
  sqlstate: z.string().nullish(),
  enqueued_at: z.iso.datetime({ offset: true }),
  started_at: z.iso.datetime({ offset: true }).nullish(),
  finished_at: z.iso.datetime({ offset: true }).nullish(),
  updated_at: z.iso.datetime({ offset: true }),
});

const jobListSchema = z.object({
  database_id: z.string(),
  jobs: z
    .array(migrationJobSchema)
    .nullable()
    .transform((value) => value ?? []),
});

const enqueueResultSchema = z.object({
  job_id: z.string(),
  execution_id: z.string().optional(),
  status: jobStatusSchema.optional(),
});

const jobResultSchema = z.object({ job_id: z.uuid() });
const operationSchema = z.enum(["APPLY", "ROLLBACK", "FORWARD"]);
const requestSchema = z.object({
  id: z.number().int().positive(),
  database_id: z.string(),
  migration_id: z.number().int().positive(),
  operation: operationSchema,
  revision: z.number().int().positive(),
  target_run_id: z.number().int().positive().optional(),
  sql_snapshot: z.string(),
  requested_by: z.string(),
  status: z.enum(["PENDING_REVIEW", "APPROVED", "QUEUED", "STALE"]),
  job_id: jobResultSchema.shape.job_id.optional(),
  viewer_id: z.string().min(1),
  can_execute: z.boolean(),
  reviews: z.array(
    z.object({
      group_id: z.string(),
      group_name: z.string(),
      leader_id: z.string().optional(),
      approved_at: z.iso.datetime({ offset: true }).optional(),
    }),
  ),
});

const apiErrorSchema = z.object({
  error: z
    .object({
      code: z.string().optional(),
      message: z.string().optional(),
    })
    .optional(),
});

export type MigrationConnection = z.infer<typeof connectionSchema>;
export type MigrationHistoryEntry = z.infer<typeof historyEntrySchema>;
export type MigrationDefinition = z.infer<typeof migrationDefinitionSchema>;
export type MigrationJob = z.infer<typeof migrationJobSchema>;
export type MigrationJobAction = z.infer<typeof jobActionSchema>;
export type MigrationJobStatus = z.infer<typeof jobStatusSchema>;
export type EnqueueResult = z.infer<typeof enqueueResultSchema>;
export type MigrationOperation = z.infer<typeof operationSchema>;
export type MigrationRequest = z.infer<typeof requestSchema>;

export type CreateMigrationInput = {
  title: string;
  description: string;
  sql_content: string;
  sql_down?: string;
  irreversible_reason?: string;
  execution_mode?: "normal" | "batch";
  batch_by_attribute?: string;
  batch_size?: number;
};

async function extractErrorMessage(
  response: Response,
  fallback: string,
): Promise<string> {
  try {
    const parsed = apiErrorSchema.safeParse(await response.json());
    if (parsed.success && parsed.data.error?.message && response.status < 500) {
      return parsed.data.error.message;
    }
  } catch {
    return fallback;
  }
  return fallback;
}

async function requestMigrationApi(
  path: string,
  init: RequestInit = {},
): Promise<unknown> {
  const response = await fetch(`/api/backend/v1${path}`, {
    cache: "no-store",
    ...init,
  });
  if (response.status === 401) {
    throw new Error("Your session expired. Sign in again.");
  }
  if (!response.ok) {
    const fallback =
      response.status === 403 || response.status === 404
        ? "This database is unavailable or you do not have access."
        : "The migration service is unavailable.";
    throw new Error(await extractErrorMessage(response, fallback));
  }
  return response.json();
}

export async function listMigrationConnections(
  signal: AbortSignal,
): Promise<MigrationConnection[]> {
  return z
    .array(connectionSchema)
    .parse(await requestMigrationApi("/connections", { signal }));
}

export async function loadMigrationHistory(
  connectionId: string,
  signal: AbortSignal,
) {
  const path = `/connections/${encodeURIComponent(connectionId)}`;
  const [connection, history] = await Promise.all([
    requestMigrationApi(path, { signal }).then((data) =>
      connectionSchema.parse(data),
    ),
    requestMigrationApi(`${path}/migrations/history`, { signal }).then((data) =>
      historySchema.parse(data),
    ),
  ]);
  if (connection.id !== connectionId || history.database_id !== connectionId) {
    throw new Error("The migration service returned a different database.");
  }
  return { connection, runs: history.runs };
}

export async function loadMigrationWorkspace(
  connectionId: string,
  signal: AbortSignal,
) {
  const [history, definitions] = await Promise.all([
    loadMigrationHistory(connectionId, signal),
    requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/migrations`,
      { signal },
    ).then((data) => definitionsSchema.parse(data)),
  ]);
  if (definitions.database_id !== connectionId) {
    throw new Error("The migration service returned a different database.");
  }
  return {
    ...history,
    migrations: definitions.migrations,
    canSubmit: definitions.can_submit,
  };
}

export async function listMigrations(
  connectionId: string,
  signal?: AbortSignal,
): Promise<MigrationDefinition[]> {
  const data = definitionsSchema.parse(
    await requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/migrations`,
      { signal },
    ),
  );
  if (data.database_id !== connectionId) {
    throw new Error("The migration service returned a different database.");
  }
  return data.migrations;
}

export async function createMigration(
  connectionId: string,
  input: CreateMigrationInput,
): Promise<MigrationDefinition> {
  const payload: Record<string, unknown> = {
    title: input.title,
    description: input.description,
    sql_content: input.sql_content,
  };
  if (input.execution_mode) {
    payload.execution_mode = input.execution_mode;
  }
  if (input.sql_down) {
    payload.sql_down = input.sql_down;
  }
  if (input.irreversible_reason) {
    payload.irreversible_reason = input.irreversible_reason;
  }
  if (input.execution_mode === "batch") {
    payload.batch_by_attribute = input.batch_by_attribute;
    payload.batch_size = input.batch_size;
  }
  return migrationDefinitionSchema.parse(
    await requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/migrations`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      },
    ),
  );
}

export async function createMigrationDraft(
  connectionId: string,
  draft: CreateMigrationInput,
): Promise<MigrationDefinition> {
  return createMigration(connectionId, draft);
}

function verifyRequest(
  data: unknown,
  connectionId: string,
  requestId?: number,
) {
  const request = requestSchema.parse(data);
  if (
    request.database_id !== connectionId ||
    (requestId !== undefined && request.id !== requestId)
  ) {
    throw new Error(
      "The migration service returned a different migration request.",
    );
  }
  return request;
}

function requestPath(connectionId: string, requestId: number) {
  return `/connections/${encodeURIComponent(connectionId)}/migration-requests/${requestId}`;
}

export async function submitMigrationRequest(
  connectionId: string,
  migrationId: number,
  revision: number,
  operation: MigrationOperation,
) {
  const request = verifyRequest(
    await requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/migrations/${migrationId}/requests`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ revision, operation }),
      },
    ),
    connectionId,
  );
  if (
    request.migration_id !== migrationId ||
    request.revision !== revision ||
    request.operation !== operation
  ) {
    throw new Error(
      "The migration service returned a different migration request.",
    );
  }
  return request;
}

export async function getMigrationRequest(
  connectionId: string,
  requestId: number,
  signal?: AbortSignal,
) {
  return verifyRequest(
    await requestMigrationApi(requestPath(connectionId, requestId), { signal }),
    connectionId,
    requestId,
  );
}

export async function approveMigrationRequest(
  connectionId: string,
  requestId: number,
  groupId: string,
) {
  return verifyRequest(
    await requestMigrationApi(
      `${requestPath(connectionId, requestId)}/approvals/${encodeURIComponent(groupId)}`,
      { method: "PUT" },
    ),
    connectionId,
    requestId,
  );
}

export async function enqueueMigrationRequest(
  connectionId: string,
  requestId: number,
) {
  return jobResultSchema.parse(
    await requestMigrationApi(`${requestPath(connectionId, requestId)}/execute`, {
      method: "POST",
    }),
  );
}

export async function enqueueMigrationAction(
  connectionId: string,
  migrationId: number,
  action: "execute" | "rollback" | "forward",
): Promise<EnqueueResult> {
  return enqueueResultSchema.parse(
    await requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/migrations/${migrationId}/${action}`,
      { method: "POST" },
    ),
  );
}

export async function listMigrationJobs(
  connectionId: string,
  signal?: AbortSignal,
  options?: { limit?: number; offset?: number },
): Promise<MigrationJob[]> {
  const params = new URLSearchParams();
  if (options?.limit !== undefined) {
    params.set("limit", String(options.limit));
  }
  if (options?.offset !== undefined) {
    params.set("offset", String(options.offset));
  }
  const query = params.toString();
  const suffix = query ? `?${query}` : "";
  const data = jobListSchema.parse(
    await requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/jobs${suffix}`,
      { signal },
    ),
  );
  if (data.database_id !== connectionId) {
    throw new Error("The migration service returned a different database.");
  }
  return data.jobs;
}

export async function getMigrationJob(
  jobId: string,
  signal?: AbortSignal,
): Promise<MigrationJob> {
  return migrationJobSchema.parse(
    await requestMigrationApi(`/jobs/${encodeURIComponent(jobId)}`, { signal }),
  );
}

export async function enqueueJobRecovery(
  connectionId: string,
  jobId: string,
  action: "resume" | "revert",
): Promise<EnqueueResult> {
  return enqueueResultSchema.parse(
    await requestMigrationApi(
      `/connections/${encodeURIComponent(connectionId)}/jobs/${encodeURIComponent(jobId)}/${action}`,
      { method: "POST" },
    ),
  );
}
