import { afterEach, describe, expect, it, vi } from "vitest";
import { buildConnectionDsn, updateConnection } from "./admin";
import type {
  ConnectionFormValues,
  ConnectionPoolValues,
} from "@/components/admin/admin-panels";

afterEach(() => {
  vi.unstubAllGlobals();
});

function connectionValues(
  engine: ConnectionFormValues["engine"],
): ConnectionFormValues {
  return {
    name: "Test database",
    engine,
    host: engine === "postgres" ? "db.example.com" : "2001:db8::1",
    port: engine === "postgres" ? 5432 : 3306,
    database: "sales data",
    username: "db user",
    password: "p@ss:word",
    sslMode: "verify-full",
    maxOpenConnections: 10,
    maxIdleConnections: 5,
    maxLifetimeSeconds: 0,
    maxIdleTimeSeconds: 0,
    connectionTimeoutSeconds: 15,
  };
}

describe("buildConnectionDsn", () => {
  it("encodes PostgreSQL credentials and database names", () => {
    expect(buildConnectionDsn(connectionValues("postgres"))).toBe(
      "postgresql://db%20user:p%40ss%3Aword@db.example.com:5432/sales%20data?sslmode=verify-full&connect_timeout=15",
    );
  });

  it("brackets IPv6 hosts and encodes PostgreSQL database path segments", () => {
    const values = connectionValues("postgres");
    values.host = "2001:db8::1";
    values.database = "sales/region";

    expect(buildConnectionDsn(values)).toBe(
      "postgresql://db%20user:p%40ss%3Aword@[2001:db8::1]:5432/sales%2Fregion?sslmode=verify-full&connect_timeout=15",
    );
  });

  it("brackets IPv6 hosts and encodes MySQL DSN credentials", () => {
    expect(buildConnectionDsn(connectionValues("mysql"))).toBe(
      "db%20user:p%40ss%3Aword@tcp([2001:db8::1]:3306)/sales%20data?timeout=15s&tls=true",
    );
  });

  it("updates only the four supported pool settings", async () => {
    const pool: ConnectionPoolValues = {
      maxOpenConnections: 8,
      maxIdleConnections: 3,
      maxLifetimeSeconds: 120,
      maxIdleTimeSeconds: 15,
    };
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        id: "connection-1",
        name: "Production",
        driver: "postgres",
        connected: true,
        pool: {
          maxOpen: 8,
          maxIdle: 3,
          maxLifetimeSeconds: 120,
          maxIdleTimeSeconds: 15,
        },
        poolStats: { OpenConnections: 3, Idle: 1 },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await updateConnection("connection-1", pool);

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/backend/v1/connections/connection-1",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          pool: {
            maxOpen: 8,
            maxIdle: 3,
            maxLifetimeSeconds: 120,
            maxIdleTimeSeconds: 15,
          },
        }),
      }),
    );
  });
});
