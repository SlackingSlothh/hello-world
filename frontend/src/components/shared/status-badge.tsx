import { Badge } from "@/components/ui/badge";

const statusVariants: Record<
  string,
  "default" | "secondary" | "destructive" | "outline"
> = {
  active: "default",
  connected: "default",
  succeeded: "default",
  success: "default",
  approved: "default",
  applied: "default",
  running: "secondary",
  queued: "secondary",
  waiting: "secondary",
  pending: "secondary",
  pending_approval: "secondary",
  "pending approval": "secondary",
  partially_committed: "destructive",
  partially_reverted: "destructive",
  partially_applied: "destructive",
  partial: "destructive",
  uncertain: "destructive",
  failed: "destructive",
  rejected: "destructive",
  disconnected: "outline",
  cancelled: "outline",
  stale: "outline",
  invalid: "destructive",
  valid: "default",
  immutable: "outline",
};

function formatStatus(status: string) {
  return status.replaceAll("_", " ");
}

export function StatusBadge({ status }: { status: string }) {
  return (
    <Badge variant={statusVariants[status.toLowerCase()] ?? "outline"}>
      {formatStatus(status)}
    </Badge>
  );
}

export function AccessBadge({ level }: { level: "view" | "view_edit" }) {
  return (
    <Badge variant={level === "view_edit" ? "default" : "secondary"}>
      {level === "view_edit" ? "View + Edit" : "View"}
    </Badge>
  );
}
