import { AdminHome } from "@/components/pages/page-clients";
import { mockAuth } from "@/lib/mock-data";

export default async function AdminPage() {
  const user = await mockAuth.requireAdmin();
  return <AdminHome isAdmin={user.isAdmin} />;
}
