import { Suspense } from "react";
import type { ReactNode } from "react";
import { headers } from "next/headers";
import { AuthenticatedLayout } from "@/components/shared/layouts";
import { LoadingState } from "@/components/shared/async-states";

export default function DatabaseRouteLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <Suspense
      fallback={<LoadingState label="Checking your session" rows={2} />}
    >
      <AuthenticatedRouteShell>{children}</AuthenticatedRouteShell>
    </Suspense>
  );
}

async function AuthenticatedRouteShell({ children }: { children: ReactNode }) {
  const requestHeaders = await headers();
  return (
    <AuthenticatedLayout
      isAdmin={requestHeaders.get("x-datadock-admin") === "true"}
      username={requestHeaders.get("x-datadock-username") ?? "Account"}
    >
      {children}
    </AuthenticatedLayout>
  );
}
