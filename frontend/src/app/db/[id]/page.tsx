import { Suspense } from "react";
import { notFound } from "next/navigation";
import type { WorkspaceTab } from "@/components/database/database-panels";
import { DatabaseWorkspace } from "@/components/pages/page-clients";
import { LoadingState } from "@/components/shared/async-states";

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
  if (!id) notFound();

  const requestedTab = tabs.has(query.tab as WorkspaceTab)
    ? (query.tab as WorkspaceTab)
    : "schema";

  return (
    <DatabaseWorkspace
      activeTab={requestedTab}
      id={id}
    />
  );
}
