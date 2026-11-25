import type { ReactNode } from "react";
import { Suspense } from "react";
import { headers } from "next/headers";
import { AdminLayout as AdminShell } from "@/components/shared/layouts";
import { LoadingState } from "@/components/shared/async-states";

export default function AdminRouteLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <Suspense
      fallback={<LoadingState label="Checking your session" rows={2} />}
    >
      <AuthenticatedAdminShell>{children}</AuthenticatedAdminShell>
    </Suspense>
  );
}

async function AuthenticatedAdminShell({ children }: { children: ReactNode }) {
  const requestHeaders = await headers();
  return (
    <AdminShell
      username={requestHeaders.get("x-datadock-username") ?? "Account"}
    >
      {children}
    </AdminShell>
  );
}
