import { notFound } from "next/navigation";
import { Suspense } from "react";
import { MigrationRequestPage } from "@/components/migration/migration-request-page";
import { LoadingState } from "@/components/shared/async-states";

type RequestPageProps = { params: Promise<{ db_name: string; request_id: string }> };

export default function RequestPage({ params }: RequestPageProps) {
  return (
    <Suspense fallback={<LoadingState label="Loading migration request" />}>
      <RequestContent params={params} />
    </Suspense>
  );
}

async function RequestContent({ params }: RequestPageProps) {
  const { db_name: connectionId, request_id } = await params;
  const requestId = Number(request_id);
  if (!/^[1-9]\d*$/.test(request_id) || !Number.isSafeInteger(requestId)) notFound();
  return <MigrationRequestPage connectionId={connectionId} key={`${connectionId}:${requestId}`} requestId={requestId} />;
}
