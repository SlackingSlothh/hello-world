import { Suspense } from "react";
import { notFound } from "next/navigation";
import type { WorkspaceTab } from "@/components/database/database-panels";
import { DatabaseWorkspace } from "@/components/pages/page-clients";
import { LoadingState } from "@/components/shared/async-states";
import { mockDatabases } from "@/lib/mock-data";

const tabs = new Set<WorkspaceTab>(["schema", "query"]);

type DatabasePageProps = {
  params: Promise<{ id: string }>;
  searchParams: Promise<{ tab?: string }>;
};

export default function DatabaseDetailPage(props: DatabasePageProps) {
  return (
    <Suspense
      fallback={<LoadingState label="Loading database workspace" rows={5} />}
    >
      <DatabaseDetailContent {...props} />
    </Suspense>
  );
}

async function DatabaseDetailContent({
  params,
  searchParams,
}: DatabasePageProps) {
  const [{ id }, query] = await Promise.all([params, searchParams]);
  const database = await mockDatabases.findById(id);
  if (!database) notFound();

  const requestedTab = tabs.has(query.tab as WorkspaceTab)
    ? (query.tab as WorkspaceTab)
    : "schema";
  const [schemas, tableRows, queryRows] = await Promise.all([
    mockDatabases.schemas(database.id),
    mockDatabases.tableRows(),
    mockDatabases.queryRows(),
  ]);

  return (
    <DatabaseWorkspace
      activeTab={requestedTab}
      database={database}
      queryRows={queryRows}
      schemas={schemas}
      tableRows={tableRows}
    />
  );
}
