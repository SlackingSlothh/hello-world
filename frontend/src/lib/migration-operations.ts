import type { MigrationDefinition, MigrationHistoryEntry, MigrationOperation } from "./api/migrations";

export function availableMigrationOperations(
  migrations: MigrationDefinition[],
  history: MigrationHistoryEntry[],
): Map<number, MigrationOperation> {
  const latestSuccess = new Map<string, MigrationHistoryEntry>();
  for (const run of history) {
    if (run.status === "SUCCEEDED" && !latestSuccess.has(run.migration_id)) {
      latestSuccess.set(run.migration_id, run);
    }
  }

  const applied = migrations.filter((item) => item.version != null)
    .sort((a, b) => (a.version ?? 0) - (b.version ?? 0));
  const nextForward = applied.find((item) => latestSuccess.get(String(item.id))?.operation === "ROLLBACK");
  const nextRollback = applied.findLast((item) => {
    const run = latestSuccess.get(String(item.id));
    return run && run.operation !== "ROLLBACK";
  });

  const operations = new Map<number, MigrationOperation>();
  if (nextForward) operations.set(nextForward.id, "FORWARD");
  if (nextRollback?.sql_down) operations.set(nextRollback.id, "ROLLBACK");
  if (!nextForward) {
    for (const item of migrations) {
      if (item.status !== "APPLIED") operations.set(item.id, "APPLY");
    }
  }
  return operations;
}
