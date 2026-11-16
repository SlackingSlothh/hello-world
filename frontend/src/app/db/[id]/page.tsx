import { Suspense } from "react";
import { notFound } from "next/navigation";
import type { WorkspaceTab } from "@/components/database/database-panels";
import { DatabaseWorkspace } from "@/components/pages/page-clients";
import { LoadingState } from "@/components/shared/async-states";
import { mockAuth, mockDatabases, mockMigrations } from "@/lib/mock-data";

const tabs = new Set<WorkspaceTab>(["schema", "query", "migration"]);

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
  await mockAuth.requireUser();
  const [{ id }, query] = await Promise.all([params, searchParams]);
  const database = await mockDatabases.findById(id);
  if (!database) notFound();

  const requestedTab = tabs.has(query.tab as WorkspaceTab)
    ? (query.tab as WorkspaceTab)
    : "schema";
  const activeTab =
    requestedTab === "migration" && database.engine !== "postgres"
      ? "schema"
      : requestedTab;
  const [
    schemas,
    tableRows,
    queryRows,
    history,
    drafts,
    requests,
    approvals,
    jobs,
    preview,
  ] = await Promise.all([
    mockDatabases.schemas(database.id),
    mockDatabases.tableRows(),
    mockDatabases.queryRows(),
    mockMigrations.list(),
    mockMigrations.drafts(),
    mockMigrations.requests(),
    mockMigrations.approvals(),
    mockMigrations.jobs(),
    mockMigrations.reviewPreview(),
  ]);
  const initialJob = jobs[0];
  if (!initialJob)
    throw new Error("Mock migration data must include an initial job.");

  return (
    <DatabaseWorkspace
      activeTab={activeTab}
      approvals={approvals}
      database={database}
      drafts={drafts}
      history={history}
      initialJob={initialJob}
      preview={preview}
      queryRows={queryRows}
      requests={requests}
      schemas={schemas}
      tableRows={tableRows}
    />
  );
}
