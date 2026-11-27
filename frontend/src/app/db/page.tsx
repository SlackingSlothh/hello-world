import { DatabaseDirectoryClient } from "@/components/pages/page-clients";
import { PageHeader } from "@/components/shared/page-header";

export default function DatabasesPage() {
  return (
    <>
      <PageHeader
        description="Databases you can browse based on your group access."
        title="Databases"
      />
      <DatabaseDirectoryClient />
    </>
  );
}
