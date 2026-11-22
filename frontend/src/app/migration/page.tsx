import { MigrationDatabaseList } from "@/components/pages/page-clients";
import { PageHeader } from "@/components/shared/page-header";
import { mockAuth, mockDatabases } from "@/lib/mock-data";

export default async function MigrationDatabasesPage() {
  await mockAuth.requireUser();
  const databases = await mockDatabases.listEditableForMigrations();
  return (
    <div className="space-y-5">
      <PageHeader
        description="PostgreSQL databases where you have permission to create and request migrations."
        title="Migration workspace"
      />
      <MigrationDatabaseList databases={databases} />
    </div>
  );
}
