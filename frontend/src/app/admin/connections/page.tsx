import { ConnectionManagement } from "@/components/pages/page-clients";
import { mockConnections } from "@/lib/mock-data";

export default async function ConnectionsPage() {
  const connections = await mockConnections.list();
  return <ConnectionManagement initialConnections={connections} />;
}
