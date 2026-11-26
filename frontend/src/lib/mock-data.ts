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

export const mockDatabases = new MockDatabaseRepository();
export const mockMigrations = new MockMigrationRepository();
