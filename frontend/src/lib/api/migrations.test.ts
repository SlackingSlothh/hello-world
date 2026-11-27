import { afterEach, expect, it, vi } from "vitest";
import {
  approveMigrationRequest,
  enqueueMigrationRequest,
  getMigrationRequest,
  loadMigrationWorkspace,
  submitMigrationRequest,
} from "./migrations";

afterEach(() => vi.unstubAllGlobals());

const pendingRequest = {
  id: 42,
  migration_id: 5,
  database_id: "sales",
  operation: "ROLLBACK",
  revision: 3,
  target_run_id: 12,
  sql_snapshot: "DROP TABLE samples;",
  requested_by: "editor",
  status: "PENDING_REVIEW",
  reviews: [{ group_id: "analytics", group_name: "Analytics", leader_id: "leader" }],
  viewer_id: "leader",
  can_execute: false,
};

it("sends only the approved request to the queue after explicit Execute", async () => {
  const jobId = "a5e43a4a-5011-4f32-a44b-2ffe7e071f0a";
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(Response.json(pendingRequest, { status: 201 }))
    .mockResolvedValueOnce(Response.json({ ...pendingRequest, status: "APPROVED" }))
    .mockResolvedValueOnce(Response.json({ ...pendingRequest, status: "APPROVED", viewer_id: "editor", can_execute: true }))
    .mockResolvedValueOnce(Response.json({ job_id: jobId }, { status: 202 }));
  vi.stubGlobal("fetch", fetchMock);

  const request = await submitMigrationRequest("sales", 5, 3, "ROLLBACK");
  expect(request.target_run_id).toBe(12);
  expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/backend/v1/connections/sales/migrations/5/requests", expect.objectContaining({
    method: "POST", body: JSON.stringify({ revision: 3, operation: "ROLLBACK" }),
  }));
  const approved = await approveMigrationRequest("sales", 42, "analytics");
  expect(approved.status).toBe("APPROVED");
  expect(approved.can_execute).toBe(false);
  expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/backend/v1/connections/sales/migration-requests/42/approvals/analytics", expect.objectContaining({ method: "PUT" }));
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect((await getMigrationRequest("sales", 42)).can_execute).toBe(true);

  expect(await enqueueMigrationRequest("sales", 42)).toEqual({ job_id: jobId });
  expect(fetchMock).toHaveBeenNthCalledWith(4, "/api/backend/v1/connections/sales/migration-requests/42/execute", expect.objectContaining({ method: "POST" }));
});

it("rejects a response for a different database", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json({ ...pendingRequest, database_id: "other" })));
  await expect(getMigrationRequest("sales", 42)).rejects.toThrow("different migration request");
});

it("opens an empty database so its editor can create the first draft", async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(Response.json({ id: "sales", name: "Sales", driver: "postgres", connected: true }))
    .mockResolvedValueOnce(Response.json({ database_id: "sales", runs: [] }))
    .mockResolvedValueOnce(Response.json({ database_id: "sales", migrations: null, can_submit: true }));
  vi.stubGlobal("fetch", fetchMock);

  const workspace = await loadMigrationWorkspace("sales", new AbortController().signal);
  expect(workspace).toMatchObject({ migrations: [], runs: [], canSubmit: true });
});
