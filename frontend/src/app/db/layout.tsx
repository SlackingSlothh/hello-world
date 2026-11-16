import type { ReactNode } from "react";
import { AuthenticatedLayout } from "@/components/shared/layouts";
import { mockAuth } from "@/lib/mock-data";

export default async function DatabaseRouteLayout({
  children,
}: {
  children: ReactNode;
}) {
  const user = await mockAuth.requireUser();
  return (
    <AuthenticatedLayout isAdmin={user.isAdmin} userName={user.name}>
      {children}
    </AuthenticatedLayout>
  );
}
