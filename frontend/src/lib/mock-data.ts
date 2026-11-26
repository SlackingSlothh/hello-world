import type {
  AccessGrant,
  ConnectionFormValues,
  ConnectionSummary,
  GroupSummary,
  MemberOption,
} from "@/components/admin/admin-panels";
import type {
  DatabaseSummary,
  SchemaNode,
} from "@/components/database/database-panels";
import type {
  ApprovalProgress,
  DraftSummary,
  JobSummary,
  MigrationRecord,
  RequestSummary,
} from "@/components/migration/migration-panels";
import { databaseRouteKey } from "@/lib/database-routing";

export type MockTableRow = {
  id: string;
  name: string;
  email: string;
  status: string;
};

export type MockQueryRow = {
  id: number;
  name: string;
  status: string;
};

abstract class MockRepository<T extends { id: string }> {
  protected constructor(private records: T[]) {}

  async list(): Promise<T[]> {
    return structuredClone(this.records);
  }

  async findById(id: string): Promise<T | undefined> {
    const record = this.records.find((item) => item.id === id);
    return record ? structuredClone(record) : undefined;
  }

  async save(record: T): Promise<T> {
    const index = this.records.findIndex((item) => item.id === record.id);
    if (index === -1) this.records.push(structuredClone(record));
    else this.records[index] = structuredClone(record);
    return structuredClone(record);
  }

  async remove(id: string): Promise<void> {
    this.records = this.records.filter((item) => item.id !== id);
  }
}

class MockConnectionRepository extends MockRepository<ConnectionSummary> {
  constructor() {
    super([
      {
        id: "conn-postgres",
        name: "Sample PostgreSQL",
        engine: "postgres",
        host: "target-postgres",
        port: 5432,
        database: "sample",
        status: "connected",
        activeConnections: 2,
        idleConnections: 4,
        lastChecked: "2026-11-13T01:00:00.000Z",
      },
      {
        id: "conn-mysql",
        name: "Sample MySQL",
        engine: "mysql",
        host: "target-mysql",
        port: 3306,
        database: "sample",
        status: "disconnected",
        activeConnections: 0,
        idleConnections: 0,
      },
      {
        id: "conn-mariadb",
        name: "Sample MariaDB",
        engine: "mariadb",
        host: "target-mariadb",
        port: 3306,
        database: "sample",
        status: "connected",
        activeConnections: 1,
        idleConnections: 2,
      },
    ]);
  }

  async testConfiguration(
    values: ConnectionFormValues,
  ): Promise<{ ok: boolean; message: string }> {
    const ok = Boolean(
      values.host.trim() && values.database.trim() && values.port > 0,
    );
    return {
      ok,
      message: ok
        ? "Mock connection test succeeded."
        : "Host, database, and port are required.",
    };
  }
}

class MockGroupRepository extends MockRepository<GroupSummary> {
  constructor() {
    super([
      {
        id: "group-analytics",
        name: "Analytics",
        leaderName: "Ari Leader",
        memberCount: 3,
        memberIds: ["user-ari", "user-bea", "user-cam"],
      },
      {
        id: "group-admins",
        name: "Administrators",
        leaderName: "Demo Administrator",
        memberCount: 2,
        memberIds: ["demo-admin", "user-cam"],
        system: true,
      },
    ]);
  }
}

class MockDatabaseRepository extends MockRepository<DatabaseSummary> {
  constructor() {
    super([
      {
        id: "conn-postgres",
        name: "Sample PostgreSQL",
        engine: "postgres",
        status: "connected",
        access: "view_edit",
        migrationRunning: true,
        awaitingReview: false,
      },
      {
        id: "conn-mysql",
        name: "Sample MySQL",
        engine: "mysql",
        status: "connected",
        access: "view",
        awaitingReview: true,
      },
      {
        id: "conn-mariadb",
        name: "Sample MariaDB",
        engine: "mariadb",
        status: "disconnected",
        access: "view",
      },
    ]);
  }

  async schemas(databaseId: string): Promise<SchemaNode[]> {
    if (databaseId === "conn-mysql" || databaseId === "conn-mariadb") {
      return [{ name: "sample", tables: ["customers", "orders", "products"] }];
    }
    return [
      { name: "public", tables: ["customers", "orders", "products"] },
      { name: "audit", tables: ["events"] },
    ];
  }

  async findByName(name: string): Promise<DatabaseSummary | undefined> {
    const databases = await this.list();
    const routeName = databaseRouteKey(name);
    return databases.find(
      (database) => databaseRouteKey(database.name) === routeName,
    );
  }

  async listEditableForMigrations(): Promise<DatabaseSummary[]> {
    const databases = await this.list();
    return databases.filter(
      (database) => database.engine === "postgres" && database.access === "view_edit",
    );
  }

  async tableRows(): Promise<MockTableRow[]> {
    return [
      {
        id: "1",
        name: "Riley Chen",
        email: "riley@example.test",
        status: "active",
      },
      {
        id: "2",
        name: "Sam Jordan",
        email: "sam@example.test",
        status: "active",
      },
      {
        id: "3",
        name: "Noah Taylor",
        email: "noah@example.test",
        status: "inactive",
      },
    ];
  }

  async queryRows(): Promise<MockQueryRow[]> {
    return [
      { id: 1, name: "Riley Chen", status: "active" },
      { id: 2, name: "Sam Jordan", status: "active" },
    ];
  }
}

class MockAccessRepository extends MockRepository<AccessGrant> {
  constructor() {
    super([
      {
        id: "grant-analytics-postgres",
        groupId: "group-analytics",
        connectionId: "conn-postgres",
        level: "view_edit",
      },
      {
        id: "grant-analytics-mysql",
        groupId: "group-analytics",
        connectionId: "conn-mysql",
        level: "view",
      },
      {
        id: "grant-admins-postgres",
        groupId: "group-admins",
        connectionId: "conn-postgres",
        level: "view",
      },
    ]);
  }
}

class MockMigrationRepository extends MockRepository<MigrationRecord> {
  constructor() {
    super([
      {
        id: "migration-001",
        sequence: 1,
        name: "Create customers",
        type: "forward",
        status: "succeeded",
        reversible: true,
        executedBy: "Ari Leader",
        executedAt: "2026-11-12T10:15:00.000Z",
        durationMs: 812,
      },
      {
        id: "migration-002",
        sequence: 2,
        name: "Add customer index",
        type: "forward",
        status: "succeeded",
        reversible: true,
        executedBy: "Demo Administrator",
        executedAt: "2026-11-12T11:30:00.000Z",
        durationMs: 132,
      },
    ]);
  }

  async drafts(): Promise<DraftSummary[]> {
    return [
      {
        id: "draft-001",
        name: "Add customer status",
        status: "valid",
        mode: "single",
      },
      {
        id: "draft-002",
        name: "Backfill customer region",
        status: "stale",
        mode: "batch",
      },
    ];
  }

  async requests(): Promise<RequestSummary[]> {
    return [
      {
        id: "request-001",
        type: "Forward migration",
        status: "pending_approval",
        requester: "Ari Leader",
        createdAt: "2026-11-13T08:30:00.000Z",
      },
    ];
  }

  async approvals(): Promise<ApprovalProgress[]> {
    return [
      {
        groupId: "group-analytics",
        groupName: "Analytics",
        leaderName: "Ari Leader",
        approved: true,
      },
      {
        groupId: "group-admins",
        groupName: "Administrators",
        leaderName: "Demo Administrator",
        approved: false,
      },
    ];
  }

  async jobs(): Promise<JobSummary[]> {
    return [
      {
        id: "job-0007",
        status: "running",
        startedAt: "2026-11-13T08:45:00.000Z",
        batchesCommitted: 3,
        batchesTotal: 10,
        rowsCommitted: 3000,
        currentCursor: "customer-3000",
      },
    ];
  }

  async reviewPreview(): Promise<string> {
    return "ALTER TABLE customers ADD COLUMN status text NOT NULL DEFAULT 'active';";
  }
}

class MockUserRepository extends MockRepository<MemberOption> {
  constructor() {
    super([
      { id: "user-ari", label: "Ari Leader (ari@example.test)" },
      { id: "user-bea", label: "Bea Analyst (bea@example.test)" },
      { id: "user-cam", label: "Cam Developer (cam@example.test)" },
      { id: "demo-admin", label: "Demo Administrator (admin@example.test)" },
    ]);
  }
}

export const mockConnections = new MockConnectionRepository();
export const mockGroups = new MockGroupRepository();
export const mockDatabases = new MockDatabaseRepository();
export const mockAccessGrants = new MockAccessRepository();
export const mockMigrations = new MockMigrationRepository();
export const mockUsers = new MockUserRepository();
