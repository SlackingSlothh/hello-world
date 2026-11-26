"use client";

import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import {
  GroupEditor,
  GroupLeaderSelect,
  GroupList,
} from "@/components/admin/admin-panels";
import { ErrorState, LoadingState } from "@/components/shared/async-states";
import { PageHeader } from "@/components/shared/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  changeGroupMembership,
  createGroup,
  listGroupMembers,
  listGroups,
  listUsers,
  setGroupLeader,
  type AccessGroup,
  type AccessUser,
  type GroupMember,
} from "@/lib/api/groups";

type Editor =
  | { mode: "create"; name: string; memberIds: string[]; leaderId: string }
  | { mode: "manage"; groupId: string; members: GroupMember[]; leaderId: string; addId: string };

export function GroupManagement() {
  const [groups, setGroups] = useState<AccessGroup[] | null>(null);
  const [users, setUsers] = useState<AccessUser[]>([]);
  const [loadError, setLoadError] = useState("");
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState(false);
  const inFlight = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    Promise.all([listGroups(signal), listUsers(signal)])
      .then(([loadedGroups, loadedUsers]) => {
        if (signal.aborted) return;
        setGroups(loadedGroups);
        setUsers(loadedUsers);
      })
      .catch((error: unknown) => {
        if (!signal.aborted) {
          setLoadError(error instanceof Error ? error.message : "Could not load groups.");
        }
      });
    return () => controller.abort();
  }, [loadAttempt]);

  async function run(action: () => Promise<void>) {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setActionError("");
    try {
      await action();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "Could not update the group.");
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  function updateGroup(groupId: string, changes: Partial<Pick<AccessGroup, "leaderId" | "memberCount">>) {
    setGroups((current) => current && current.map((group) =>
      group.id === groupId ? { ...group, ...changes } : group,
    ));
  }

  const group = editor?.mode === "manage"
    ? groups?.find((group) => group.id === editor.groupId)
    : undefined;
  const currentMembers = editor?.mode === "manage" ? editor.members.filter((user) => user.member) : [];
  const availableUsers = editor?.mode === "manage" ? editor.members.filter((user) => !user.member) : [];
  const leaderOptions = currentMembers
    .filter((user) => user.active)
    .map((user) => ({ id: user.id, label: user.username }));

  function changeMember(userId: string, member: boolean) {
    if (editor?.mode !== "manage" || !group || !userId) return;
    void run(async () => {
      await changeGroupMembership(group.id, userId, member);
      const members = editor.members.map((user) =>
        user.id === userId ? { ...user, member } : user,
      );
      setEditor({
        ...editor,
        members,
        leaderId: !member && userId === editor.leaderId ? group.leaderId ?? "" : editor.leaderId,
        addId: "",
      });
      updateGroup(group.id, { memberCount: members.filter((user) => user.member).length });
      toast.success(member ? "Member added." : "Member removed.");
    });
  }

  const usernames = new Map(users.map((user) => [user.id, user.username]));

  return (
    <div className="space-y-5">
      <PageHeader
        title="Groups"
        actions={
          <Button
            disabled={busy || !groups}
            onClick={() => {
              setActionError("");
              setEditor(editor ? null : { mode: "create", name: "", memberIds: [], leaderId: "" });
            }}
          >
            {editor ? "Close form" : "Create group"}
          </Button>
        }
      />
      {loadError && (
        <ErrorState
          message={loadError}
          onRetry={() => {
            setLoadError("");
            setLoadAttempt((attempt) => attempt + 1);
          }}
        />
      )}
      {actionError && <ErrorState message={actionError} />}
      {!groups && !loadError && <LoadingState label="Loading groups" />}
      {editor?.mode === "create" && (
        <Card>
          <CardHeader><CardTitle>New group</CardTitle></CardHeader>
          <CardContent>
            <GroupEditor
              busy={busy}
              leaderId={editor.leaderId}
              members={users.map((user) => ({
                ...user,
                label: user.active ? user.username : `${user.username} (inactive)`,
              }))}
              name={editor.name}
              onLeaderChange={(leaderId) => setEditor({ ...editor, leaderId })}
              onMembersChange={(memberIds) => setEditor({
                ...editor,
                memberIds,
                leaderId: memberIds.includes(editor.leaderId) ? editor.leaderId : "",
              })}
              onNameChange={(name) => setEditor({ ...editor, name })}
              onSubmit={() => void run(async () => {
                const created = await createGroup({
                  name: editor.name.trim(),
                  memberIds: editor.memberIds,
                  leaderId: editor.leaderId,
                });
                setGroups((current) => [...(current ?? []), created].sort((a, b) => a.name.localeCompare(b.name)));
                setEditor(null);
                toast.success("Group created.");
              })}
              selectedMemberIds={editor.memberIds}
            />
          </CardContent>
        </Card>
      )}
      {editor?.mode === "manage" && group && (
        <Card>
          <CardHeader><CardTitle>Manage {group.name}</CardTitle></CardHeader>
          <CardContent>
            <fieldset className="space-y-5" disabled={busy}>
              <legend className="sr-only">Manage {group.name}</legend>
              <section className="space-y-2">
                <h2 className="text-sm font-medium">Group members</h2>
                {currentMembers.length === 0 ? (
                  <p className="text-sm text-muted-foreground">No members yet.</p>
                ) : (
                  <ul className="max-h-64 divide-y overflow-y-auto rounded-md border">
                    {currentMembers.map((user) => (
                      <li className="flex items-center justify-between gap-3 p-2 text-sm" key={user.id}>
                        <span className="min-w-0 break-words">
                          {user.username}{!user.active && " (inactive)"}
                        </span>
                        {user.id === group.leaderId ? (
                          <span className="shrink-0 text-muted-foreground">Current leader</span>
                        ) : (
                          <Button
                            aria-label={`Remove ${user.username}`}
                            onClick={() => changeMember(user.id, false)}
                            size="sm"
                            type="button"
                            variant="outline"
                          >
                            Remove
                          </Button>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
                <form
                  className="flex flex-col gap-2 sm:flex-row"
                  onSubmit={(event) => {
                    event.preventDefault();
                    changeMember(editor.addId, true);
                  }}
                >
                  <select
                    aria-label="Add member"
                    className="h-9 w-full min-w-0 rounded-md border bg-background px-3 text-sm"
                    disabled={availableUsers.length === 0}
                    onChange={(event) => setEditor({ ...editor, addId: event.target.value })}
                    value={editor.addId}
                  >
                    <option value="">Choose a user</option>
                    {availableUsers.map((user) => (
                      <option key={user.id} value={user.id}>{user.username}{!user.active && " (inactive)"}</option>
                    ))}
                  </select>
                  <Button disabled={!editor.addId} type="submit" variant="outline">Add member</Button>
                </form>
              </section>
              <form
                className="space-y-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  void run(async () => {
                    await setGroupLeader(group.id, editor.leaderId);
                    updateGroup(group.id, { leaderId: editor.leaderId });
                    toast.success("Group leader saved.");
                  });
                }}
              >
                <GroupLeaderSelect
                  leaderId={leaderOptions.some((user) => user.id === editor.leaderId) ? editor.leaderId : ""}
                  members={leaderOptions}
                  onLeaderChange={(leaderId) => setEditor({ ...editor, leaderId })}
                />
                <Button
                  disabled={editor.leaderId === group.leaderId || !leaderOptions.some((user) => user.id === editor.leaderId)}
                  type="submit"
                >
                  Save leader
                </Button>
              </form>
            </fieldset>
          </CardContent>
        </Card>
      )}
      {groups && (
        <GroupList
          busy={busy}
          groups={groups.map((group) => ({
            ...group,
            leaderName: group.leaderId ? usernames.get(group.leaderId) ?? group.leaderId : "Not assigned",
            system: group.isAdmin,
          }))}
          onEdit={(group) => void run(async () => {
            const members = await listGroupMembers(group.id);
            setUsers(members);
            setEditor({
              mode: "manage",
              groupId: group.id,
              members,
              leaderId: groups.find((current) => current.id === group.id)?.leaderId ?? "",
              addId: "",
            });
          })}
        />
      )}
    </div>
  );
}
