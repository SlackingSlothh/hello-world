import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createMigration,
  enqueueJobRecovery,
  enqueueMigrationAction,
  getMigrationJob,
  listMigrationJobs,
  loadMigrationHistory,
} from "./migrations";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("migration api client", () => {
  it("loads migration history including batch recovery operations", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          id: "db-1",
          name: "Orders",
          driver: "postgres",
          connected: true,
        }),
      )
      .mockResolvedValueOnce(
        Response.json({
          database_id: "db-1",
          runs: [
            {
              id: "101",
              migration_id: "7",
              title: "Backfill status",
              reversible: true,
              version: 1,
              attempt_number: 1,
              revision: 1,
              operation: "RESUME_BATCH",
              status: "SUCCEEDED",
              sql_snapshot: "UPDATE orders SET status = 'done' WHERE id > $1 AND id <= $2",
              sql_dispatch_started: true,
              executed_by: "user-1",
              username: "alice",
              started_at: "2026-11-27T01:00:00Z",
              finished_at: "2026-11-27T01:00:05Z",
              execution_ms: 5000,
            },
          ],
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const controller = new AbortController();
    const result = await loadMigrationHistory("db-1", controller.signal);

    expect(result.connection.name).toBe("Orders");
    expect(result.runs).toHaveLength(1);
    expect(result.runs[0]?.operation).toBe("RESUME_BATCH");
  });

  it("creates a batch migration draft with cursor attribute and batch size", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json(
        {
          id: 12,
          database_id: "db-1",
          title: "Batch backfill",
          description: "Chunked update",
          sql_content: "UPDATE items SET flag = true WHERE id > $1 AND id <= $2",
          sql_down: "UPDATE items SET flag = false WHERE id >= $1 AND id <= $2",
          execution_mode: "batch",
          batch_by_attribute: "id",
          batch_size: 500,
          revision: 1,
          status: "DRAFT",
          created_by: "user-1",
          updated_by: "user-1",
          created_at: "2026-11-27T01:00:00Z",
          updated_at: "2026-11-27T01:00:00Z",
        },
        { status: 201 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const created = await createMigration("db-1", {
      title: "Batch backfill",
      description: "Chunked update",
      sql_content: "UPDATE items SET flag = true WHERE id > $1 AND id <= $2",
      sql_down: "UPDATE items SET flag = false WHERE id >= $1 AND id <= $2",
      execution_mode: "batch",
      batch_by_attribute: "id",
      batch_size: 500,
    });

    expect(created.execution_mode).toBe("batch");
    expect(created.batch_by_attribute).toBe("id");
    expect(created.batch_size).toBe(500);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/backend/v1/connections/db-1/migrations",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          title: "Batch backfill",
          description: "Chunked update",
          sql_content: "UPDATE items SET flag = true WHERE id > $1 AND id <= $2",
          execution_mode: "batch",
          sql_down: "UPDATE items SET flag = false WHERE id >= $1 AND id <= $2",
          batch_by_attribute: "id",
          batch_size: 500,
        }),
      }),
    );
  });

  it("enqueues execute, rollback, and forward actions with 202 response", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json(
        {
          job_id: "job-1",
          execution_id: "exec-1",
          status: "pending",
        },
        { status: 202 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const res = await enqueueMigrationAction("db-1", 12, "execute");
    expect(res).toEqual({
      job_id: "job-1",
      execution_id: "exec-1",
      status: "pending",
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/backend/v1/connections/db-1/migrations/12/execute",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("parses partial and uncertain job details and lists jobs", async () => {
    const sampleJob = {
      job_id: "job-99",
      migration_id: 12,
      execution_id: "exec-99",
      database_id: "db-1",
      actor_id: "user-1",
      actor_username: "alice",
      action: "execute",
      mode: "batch",
      status: "partial",
      batch_by_attribute: "id",
      batch_size: 1000,
      target_scope_max: "50000",
      last_committed_batch: 3,
      last_committed_cursor: "3000",
      committed_rows: 3000,
      attempt: 1,
      max_retries: 3,
      error_message: "check constraint violated",
      sqlstate: "23514",
      enqueued_at: "2026-11-27T01:00:00Z",
      started_at: "2026-11-27T01:00:01Z",
      finished_at: "2026-11-27T01:00:04Z",
      updated_at: "2026-11-27T01:00:04Z",
    };

    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({ database_id: "db-1", jobs: [sampleJob] }),
      )
      .mockResolvedValueOnce(Response.json(sampleJob));
    vi.stubGlobal("fetch", fetchMock);

    const jobs = await listMigrationJobs("db-1", undefined, { limit: 20, offset: 0 });
    expect(jobs).toHaveLength(1);
    expect(jobs[0]?.status).toBe("partial");
    expect(jobs[0]?.last_committed_cursor).toBe("3000");

    const single = await getMigrationJob("job-99");
    expect(single.sqlstate).toBe("23514");
  });

  it("enqueues batch resume and revert recovery and surfaces backend errors", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json(
          { job_id: "job-100", execution_id: "exec-99", status: "pending" },
          { status: 202 },
        ),
      )
      .mockResolvedValueOnce(
        Response.json(
          { error: { code: "access_denied", message: "access to this migration is denied" } },
          { status: 403 },
        ),
      );
    vi.stubGlobal("fetch", fetchMock);

    const resumed = await enqueueJobRecovery("db-1", "job-99", "resume");
    expect(resumed.job_id).toBe("job-100");
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/backend/v1/connections/db-1/jobs/job-99/resume",
      expect.objectContaining({ method: "POST" }),
    );

    await expect(enqueueJobRecovery("db-1", "job-99", "revert")).rejects.toThrow(
      "access to this migration is denied",
    );
  });
});
