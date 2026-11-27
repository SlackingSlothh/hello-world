import { afterEach, describe, expect, it, vi } from "vitest";
import {
  executeDatabaseQuery,
  listDatabaseConnections,
  loadDatabaseRows,
  loadDatabaseSchemas,
} from "./database";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("database API", () => {
  it("lists only backend-authorized connections as database summaries", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json([
        {
          id: "sales-db",
          name: "Sales",
          driver: "postgres",
          connected: true,
        },
      ]),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(listDatabaseConnections()).resolves.toEqual([
      {
        id: "sales-db",
        name: "Sales",
        engine: "postgres",
        status: "connected",
      },
    ]);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/backend/v1/connections",
      expect.objectContaining({ cache: "no-store" }),
    );
  });

  it("loads real schema and table names", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json([{ name: "sales" }]))
      .mockResolvedValueOnce(
        Response.json([
          { schema: "sales", name: "orders", type: "BASE TABLE" },
        ]),
      );
    vi.stubGlobal("fetch", fetchMock);

    await expect(loadDatabaseSchemas("sales-db")).resolves.toEqual([
      { name: "sales", tables: ["orders"] },
    ]);
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/backend/v1/connections/sales-db/schemas",
      expect.any(Object),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/backend/v1/connections/sales-db/schemas/sales/tables",
      expect.any(Object),
    );
  });

  it("requests bounded, sorted table pages and sends queries to the backend", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          columns: ["id"],
          rows: [[1]],
          limit: 51,
          offset: 50,
        }),
      )
      .mockResolvedValueOnce(Response.json({ columns: ["answer"], rows: [[42]] }));
    vi.stubGlobal("fetch", fetchMock);

    await loadDatabaseRows("sales-db", "sales", "orders", {
      limit: 51,
      offset: 50,
      sort: "id",
      direction: "desc",
    });
    await executeDatabaseQuery("sales-db", "SELECT 42;");

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/backend/v1/connections/sales-db/schemas/sales/tables/orders/rows?limit=51&offset=50&sort=id&direction=desc",
      expect.any(Object),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/backend/v1/connections/sales-db/query",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ sql: "SELECT 42;", args: [], limit: 500 }),
      }),
    );
  });
});
