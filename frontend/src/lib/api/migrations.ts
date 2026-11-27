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

export type MigrationConnection = z.infer<typeof connectionSchema>;
export type MigrationHistoryEntry = z.infer<typeof historyEntrySchema>;

async function readMigrations(path: string, signal: AbortSignal) {
  const response = await fetch(`/api/backend/v1/connections${path}`, {
    cache: "no-store",
    signal,
  });
  if (response.status === 401) throw new Error("Your session expired. Sign in again.");
  if (response.status === 403 || response.status === 404) {
    throw new Error("This database is unavailable or you do not have access.");
  }
  if (!response.ok) throw new Error("The migration service is unavailable.");
  return response.json();
}

export async function listMigrationConnections(signal: AbortSignal): Promise<MigrationConnection[]> {
  return z.array(connectionSchema).parse(await readMigrations("", signal));
}

export async function loadMigrationHistory(connectionId: string, signal: AbortSignal) {
  const path = `/${encodeURIComponent(connectionId)}`;
  const [connection, history] = await Promise.all([
    readMigrations(path, signal).then((data) => connectionSchema.parse(data)),
    readMigrations(`${path}/migrations/history`, signal).then((data) => historySchema.parse(data)),
  ]);
  if (connection.id !== connectionId || history.database_id !== connectionId) {
    throw new Error("The migration service returned a different database.");
  }
  return { connection, runs: history.runs };
}
