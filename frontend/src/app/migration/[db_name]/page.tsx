import { Suspense } from "react";
import { notFound } from "next/navigation";
import { MigrationDatabaseWorkspace } from "@/components/pages/page-clients";
import { LoadingState } from "@/components/shared/async-states";
import { mockDatabases, mockMigrations } from "@/lib/mock-data";

type MigrationPageProps = {
  params: Promise<{ db_name: string }>;
};

export default function MigrationDatabasePage(props: MigrationPageProps) {
  return (
    <Suspense
      fallback={<LoadingState label="Loading migration workspace" rows={6} />}
    >
      <MigrationDatabaseContent {...props} />
    </Suspense>
  );
}

async function MigrationDatabaseContent({ params }: MigrationPageProps) {
  const { db_name: dbName } = await params;
  const database = await mockDatabases.findByName(dbName);
  if (!database || database.engine !== "postgres" || database.access !== "view_edit") {
    notFound();
  }

  const [history, drafts, requests, approvals, jobs, preview] = await Promise.all([
    mockMigrations.list(),
    mockMigrations.drafts(),
    mockMigrations.requests(),
    mockMigrations.approvals(),
    mockMigrations.jobs(),
    mockMigrations.reviewPreview(),
  ]);
  const initialJob = jobs[0];
  if (!initialJob) {
    throw new Error("Mock migration data must include an initial job.");
  }

  return (
    <MigrationDatabaseWorkspace
      approvals={approvals}
      database={database}
      drafts={drafts}
      history={history}
      initialJob={initialJob}
      preview={preview}
      requests={requests}
    />
  );
}
