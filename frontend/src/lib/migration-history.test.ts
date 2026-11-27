import { expect, it } from "vitest";
import type { MigrationHistoryEntry } from "./api/migrations";
import { visibleMigrationHistory } from "./migration-history";

it("hides only successful rollback pairs and preserves the backend order", () => {
  function run(
    id: string,
    operation: MigrationHistoryEntry["operation"],
    status: MigrationHistoryEntry["status"],
    rollback_target_run_id?: string,
  ): MigrationHistoryEntry {
    return {
      id, operation, status, rollback_target_run_id,
      migration_id: "10", title: "Change products", reversible: true, revision: 2,
      attempt_number: 1, sql_snapshot: "SELECT 1", executed_by: "editor",
      username: "Editor", started_at: "2026-11-27T01:00:00Z", execution_ms: 10,
    };
  }
  const entries = [
    run("9007199254741000", "FORWARD", "SUCCEEDED"),
    run("9007199254740999", "ROLLBACK", "RUNNING", "9007199254741000"),
    run("9007199254740998", "ROLLBACK", "SUCCEEDED", "9007199254740992"),
    run("9007199254740997", "ROLLBACK", "FAILED", "9007199254740992"),
    run("9007199254740993", "APPLY", "SUCCEEDED"),
    run("9007199254740992", "APPLY", "SUCCEEDED"),
  ];

  expect(visibleMigrationHistory(entries, false)).toEqual(entries);
  expect(visibleMigrationHistory(entries, true).map((entry) => entry.id)).toEqual([
    "9007199254741000", "9007199254740999", "9007199254740997", "9007199254740993",
  ]);
  expect(entries).toHaveLength(6);
});
