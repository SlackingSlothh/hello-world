import type { ReactNode } from "react";
import { AdminLayout as AdminShell } from "@/components/shared/layouts";
import { mockAuth } from "@/lib/mock-data";

export default async function AdminRouteLayout({ children }: { children: ReactNode }) {
  const user = await mockAuth.requireAdmin();
  return <AdminShell userName={user.name}>{children}</AdminShell>;
}
