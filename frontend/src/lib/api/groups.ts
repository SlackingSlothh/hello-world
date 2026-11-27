export type AccessGroup = {
  id: string;
  name: string;
  leaderId: string | null;
  isAdmin: boolean;
  memberCount: number;
};

export type AccessUser = {
  id: string;
  username: string;
  active: boolean;
};

export type GroupMember = AccessUser & { member: boolean };

type CreateGroupRequest = {
  name: string;
  memberIds: string[];
  leaderId: string;
};

async function accessRequest(path: string, options?: RequestInit) {
  let response: Response;
  try {
    response = await fetch(`/api/admin/access/${path}`, {
      ...options,
      cache: "no-store",
    });
  } catch (error) {
    if (error instanceof TypeError) {
      throw new Error("Could not reach the access management service.");
    }
    throw error;
  }

  if (!response.ok) {
    if (response.status === 401) throw new Error("Your session expired. Sign in again.");
    if (response.status === 403) throw new Error("This admin action was denied.");
    const payload = await response.json().catch(() => null);
    throw new Error(payload?.error?.message ?? `Access request failed (HTTP ${response.status}).`);
  }
  return response;
}

export async function listGroups(signal: AbortSignal): Promise<AccessGroup[]> {
  const response = await accessRequest("groups", { signal });
  const { groups } = await response.json();
  return groups;
}

export async function listUsers(signal: AbortSignal): Promise<AccessUser[]> {
  const response = await accessRequest("users", { signal });
  return response.json();
}

export async function createUser(username: string, password: string): Promise<AccessUser> {
  const response = await accessRequest("users", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ username, password }),
  });
  return response.json();
}

export async function createGroup(input: CreateGroupRequest): Promise<AccessGroup> {
  const response = await accessRequest("groups", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  return response.json();
}

export async function listGroupMembers(groupId: string): Promise<GroupMember[]> {
  const response = await accessRequest(`groups/${encodeURIComponent(groupId)}/members`);
  return response.json();
}

export async function changeGroupMembership(groupId: string, userId: string, member: boolean) {
  await accessRequest(`groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`, {
    method: member ? "PUT" : "DELETE",
  });
}

export async function setGroupLeader(groupId: string, leaderId: string) {
  await accessRequest(`groups/${encodeURIComponent(groupId)}/leader`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ leaderId }),
  });
}
