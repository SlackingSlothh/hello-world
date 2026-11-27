"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { MigrationActions } from "@/components/migration/migration-actions";
import { MigrationHistory } from "@/components/migration/migration-panels";
import { EmptyState, ErrorState, LoadingState } from "@/components/shared/async-states";
import { PageHeader } from "@/components/shared/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  listMigrationConnections,
  loadMigrationWorkspace,
  type MigrationConnection,
} from "@/lib/api/migrations";

type PageData =
  | { connections: MigrationConnection[] }
  | Awaited<ReturnType<typeof loadMigrationWorkspace>>;

export function MigrationHistoryPage({ connectionId }: { connectionId?: string }) {
  const [data, setData] = useState<PageData>();
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    const request = connectionId
      ? loadMigrationWorkspace(connectionId, signal)
      : listMigrationConnections(signal).then((connections) => ({ connections }));
    request.then(
      (result) => {
        if (!signal.aborted) setData(result);
      },
      (error: unknown) => {
        if (signal.aborted) return;
        setError(error instanceof Error && error.name !== "ZodError"
          ? error.message
          : "Could not load migration data.");
      },
    );
    return () => controller.abort();
  }, [connectionId, retry]);

  return (
    <div className="space-y-5">
      <PageHeader
        breadcrumbs={connectionId ? [{ label: "Migrations", href: "/migration" }] : undefined}
        title={data && "connection" in data ? `Migrations · ${data.connection.name}` : "Migrations"}
      />
      {error ? (
        <ErrorState message={error} onRetry={() => {
          setError("");
          setRetry((attempt) => attempt + 1);
        }} />
      ) : !data ? (
        <LoadingState label={connectionId ? "Loading migration history" : "Loading databases"} />
      ) : "runs" in data ? (
        <>
          <MigrationActions
            canSubmit={data.canSubmit}
            connectionId={data.connection.id}
            migrations={data.migrations}
            onDraftCreated={(draft) => setData((current) => current && "migrations" in current
              ? { ...current, migrations: [draft, ...current.migrations] }
              : current)}
            runs={data.runs}
          />
          <section aria-labelledby="migration-history" className="space-y-4">
            <h2 id="migration-history" className="text-lg font-semibold">Migration history</h2>
            <MigrationHistory records={data.runs} />
          </section>
        </>
      ) : data.connections.length === 0 ? (
        <EmptyState title="No databases available" description="A database appears here when your group has access." />
      ) : (
        <ul className="grid gap-4 md:grid-cols-2">
          {data.connections.map((connection) => (
            <li key={connection.id} className="min-w-0">
              <Card>
                <CardHeader>
                  <CardTitle className="break-words">{connection.name}</CardTitle>
                  <p className="text-sm text-muted-foreground">
                    {connection.driver} · {connection.connected ? "Connected" : "Disconnected"}
                  </p>
                  <p className="break-all font-mono text-xs text-muted-foreground">{connection.id}</p>
                </CardHeader>
                <CardContent>
                  <Link
                    className="inline-flex h-8 items-center rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    href={`/migration/${encodeURIComponent(connection.id)}`}
                  >
                    Open migrations
                  </Link>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
