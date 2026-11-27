import { expect, it } from "vitest";
import type { MigrationDefinition, MigrationHistoryEntry } from "./api/migrations";
import { availableMigrationOperations } from "./migration-operations";

const applied: MigrationDefinition[] = [1, 2, 3].map((version) => ({
  id: version, version, revision: 1, title: `v${version}`, status: "APPLIED", sql_down: "DROP TABLE sample;",
}));
const draft: MigrationDefinition = { id: 4, revision: 1, title: "New draft", status: "DRAFT" };

function run(id: number, version: number, operation: MigrationHistoryEntry["operation"], status: MigrationHistoryEntry["status"] = "SUCCEEDED"): MigrationHistoryEntry {
  return {
    id: String(id), migration_id: String(version), title: `v${version}`, reversible: true, version, attempt_number: 1,
    revision: 1, operation, status, sql_snapshot: "SELECT 1", executed_by: "editor", username: "Editor",
    started_at: "2026-11-27T00:00:00Z", execution_ms: 1,
  };
}

it("offers only the latest active rollback and earliest undone forward", () => {
  const history = [run(6, 3, "FORWARD", "FAILED"), run(5, 2, "ROLLBACK"), run(4, 3, "ROLLBACK"),
    run(3, 3, "APPLY"), run(2, 2, "APPLY"), run(1, 1, "APPLY")];

  expect(availableMigrationOperations([...applied, draft], history)).toEqual(new Map([
    [2, "FORWARD"], [1, "ROLLBACK"],
  ]));
});

it("offers Apply after forward is no longer pending and leaves an irreversible latest version active", () => {
  const history = [run(4, 3, "FORWARD"), run(3, 3, "ROLLBACK"), run(2, 2, "APPLY"), run(1, 1, "APPLY")];
  const migrations = [...applied.slice(0, 2), { ...applied[2], sql_down: undefined }, draft];

  expect(availableMigrationOperations(migrations, history)).toEqual(new Map([[4, "APPLY"]]));
});
