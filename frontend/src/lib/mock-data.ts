import type {
  DatabaseSummary,
  SchemaNode,
} from "@/components/database/database-panels";
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

export const mockDatabases = new MockDatabaseRepository();
