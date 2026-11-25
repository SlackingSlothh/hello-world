import { DatabaseList } from "@/components/database/database-panels";
import { PageHeader } from "@/components/shared/page-header";
import { mockDatabases } from "@/lib/mock-data";

export default async function DatabasesPage() {
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
