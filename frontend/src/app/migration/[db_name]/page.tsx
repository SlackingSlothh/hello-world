import { Suspense } from "react";
import { MigrationHistoryPage } from "@/components/migration/migration-history-page";
import { LoadingState } from "@/components/shared/async-states";

type MigrationPageProps = {
  params: Promise<{ db_name: string }>;
};

export default function MigrationDatabasePage({ params }: MigrationPageProps) {
  return (
    <Suspense fallback={<LoadingState label="Loading migration history" />}>
      <MigrationDatabaseContent params={params} />
    </Suspense>
  );
}

async function MigrationDatabaseContent({ params }: MigrationPageProps) {
  const { db_name: connectionId } = await params;
  return <MigrationHistoryPage connectionId={connectionId} key={connectionId} />;
}
