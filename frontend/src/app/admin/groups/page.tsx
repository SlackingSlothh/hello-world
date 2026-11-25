import { GroupManagement } from "@/components/pages/page-clients";
import { mockGroups, mockUsers } from "@/lib/mock-data";

export default async function GroupsPage() {
  const [groups, members] = await Promise.all([
    mockGroups.list(),
    mockUsers.list(),
  ]);
  return <GroupManagement initialGroups={groups} members={members} />;
}
