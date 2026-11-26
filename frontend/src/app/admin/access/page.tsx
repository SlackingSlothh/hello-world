import { AccessManagement } from "@/components/pages/page-clients";
import { mockAccessGrants, mockConnections, mockGroups } from "@/lib/mock-data";

export default async function AccessPage() {
  const [groups, connections, grants] = await Promise.all([
    mockGroups.list(),
    mockConnections.list(),
    mockAccessGrants.list(),
  ]);
  return (
    <AccessManagement
      connections={connections.map(({ id, name }) => ({ id, name }))}
      groups={groups.map(({ id, name }) => ({ id, name }))}
      initialGrants={grants}
    />
  );
}
