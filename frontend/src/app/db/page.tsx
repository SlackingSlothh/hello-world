import { DatabaseList } from "@/components/database/database-panels";
import { PageHeader } from "@/components/shared/page-header";
import { mockAuth, mockDatabases } from "@/lib/mock-data";

export default async function DatabasesPage() {
  await mockAuth.requireUser();
  const databases = await mockDatabases.list();
  return (
    <>
      <PageHeader
        description="Databases you can browse based on your group access."
        title="Databases"
      />
      <DatabaseList databases={databases} />
    </>
  );
}
