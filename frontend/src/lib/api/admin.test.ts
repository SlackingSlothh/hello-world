import { describe, expect, it } from "vitest";
import { buildConnectionDsn } from "./admin";
import type { ConnectionFormValues } from "@/components/admin/admin-panels";

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
});
