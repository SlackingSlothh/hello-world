import type { MigrationHistoryEntry } from "./api/migrations";

export function visibleMigrationHistory(entries: MigrationHistoryEntry[], hideRollbacks: boolean) {
  if (!hideRollbacks) return entries;

  const hidden = new Set<string>();
  for (const entry of entries) {
    if (entry.operation === "ROLLBACK" && entry.status === "SUCCEEDED" && entry.rollback_target_run_id) {
      hidden.add(entry.id);
      hidden.add(entry.rollback_target_run_id);
    }
  }
  return entries.filter((entry) => !hidden.has(entry.id));
}
