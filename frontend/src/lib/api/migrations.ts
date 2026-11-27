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
  operation: z.enum(["APPLY", "ROLLBACK", "FORWARD"]),
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
  runs: z.array(historyEntrySchema),
});

const migrationDefinitionSchema = z.object({
  id: z.number().int().positive(),
  title: z.string(),
  revision: z.number().int().positive(),
  version: z.number().int().positive().nullish(),
  status: z.enum(["DRAFT", "FAILED", "APPLIED"]),
  sql_down: z.string().optional(),
});

const definitionsSchema = z.object({
  database_id: z.string(),
  can_submit: z.boolean(),
  migrations: z.array(migrationDefinitionSchema).nullable().transform((migrations) => migrations ?? []),
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
  reviews: z.array(z.object({
    group_id: z.string(),
    group_name: z.string(),
    leader_id: z.string().optional(),
    approved_at: z.iso.datetime({ offset: true }).optional(),
  })),
});

export type MigrationConnection = z.infer<typeof connectionSchema>;
export type MigrationHistoryEntry = z.infer<typeof historyEntrySchema>;
export type MigrationDefinition = z.infer<typeof migrationDefinitionSchema>;
export type MigrationOperation = z.infer<typeof operationSchema>;
export type MigrationRequest = z.infer<typeof requestSchema>;

async function requestMigrations(path: string, options?: RequestInit) {
  const response = await fetch(`/api/backend/v1/connections${path}`, {
    cache: "no-store",
    ...options,
  });
  if (response.status === 401) throw new Error("Your session expired. Sign in again.");
  if (!response.ok) {
    const payload: unknown = await response.json().catch(() => null);
    const error = z.object({ error: z.object({ message: z.string() }) }).safeParse(payload);
    if (error.success && response.status < 500) throw new Error(error.data.error.message);
    if (response.status === 403 || response.status === 404) {
      throw new Error("This database is unavailable or you do not have access.");
    }
    throw new Error("The migration service is unavailable.");
  }
  return response.json();
}

export async function listMigrationConnections(signal: AbortSignal): Promise<MigrationConnection[]> {
  return z.array(connectionSchema).parse(await requestMigrations("", { signal }));
}

export async function loadMigrationHistory(connectionId: string, signal: AbortSignal) {
  const path = `/${encodeURIComponent(connectionId)}`;
  const [connection, history] = await Promise.all([
    requestMigrations(path, { signal }).then((data) => connectionSchema.parse(data)),
    requestMigrations(`${path}/migrations/history`, { signal }).then((data) => historySchema.parse(data)),
  ]);
  if (connection.id !== connectionId || history.database_id !== connectionId) {
    throw new Error("The migration service returned a different database.");
  }
  return { connection, runs: history.runs };
}

export async function loadMigrationWorkspace(connectionId: string, signal: AbortSignal) {
  const [history, definitions] = await Promise.all([
    loadMigrationHistory(connectionId, signal),
    requestMigrations(`/${encodeURIComponent(connectionId)}/migrations`, { signal })
      .then((data) => definitionsSchema.parse(data)),
  ]);
  if (definitions.database_id !== connectionId) throw new Error("The migration service returned a different database.");
  return { ...history, migrations: definitions.migrations, canSubmit: definitions.can_submit };
}

export async function createMigrationDraft(connectionId: string, draft: {
  title: string;
  description: string;
  sql_content: string;
  sql_down?: string;
  irreversible_reason?: string;
}) {
  return migrationDefinitionSchema.parse(await requestMigrations(`/${encodeURIComponent(connectionId)}/migrations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(draft),
  }));
}

function verifyRequest(data: unknown, connectionId: string, requestId?: number) {
  const request = requestSchema.parse(data);
  if (request.database_id !== connectionId || (requestId !== undefined && request.id !== requestId)) {
    throw new Error("The migration service returned a different migration request.");
  }
  return request;
}

function requestPath(connectionId: string, requestId: number) {
  return `/${encodeURIComponent(connectionId)}/migration-requests/${requestId}`;
}

export async function submitMigrationRequest(connectionId: string, migrationId: number, revision: number, operation: MigrationOperation) {
  const request = verifyRequest(await requestMigrations(`/${encodeURIComponent(connectionId)}/migrations/${migrationId}/requests`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ revision, operation }),
  }), connectionId);
  if (request.migration_id !== migrationId || request.revision !== revision || request.operation !== operation) {
    throw new Error("The migration service returned a different migration request.");
  }
  return request;
}

export async function getMigrationRequest(connectionId: string, requestId: number, signal?: AbortSignal) {
  return verifyRequest(await requestMigrations(requestPath(connectionId, requestId), { signal }), connectionId, requestId);
}

export async function approveMigrationRequest(connectionId: string, requestId: number, groupId: string) {
  return verifyRequest(await requestMigrations(`${requestPath(connectionId, requestId)}/approvals/${encodeURIComponent(groupId)}`, {
    method: "PUT",
  }), connectionId, requestId);
}

export async function enqueueMigrationRequest(connectionId: string, requestId: number) {
  return jobResultSchema.parse(await requestMigrations(`${requestPath(connectionId, requestId)}/execute`, { method: "POST" }));
}
