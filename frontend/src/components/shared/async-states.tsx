import { AlertCircle, RefreshCw } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";

export function LoadingState({
  label = "Loading",
  rows = 3,
}: {
  label?: string;
  rows?: number;
}) {
  return (
    <section aria-busy="true" aria-label={label} className="space-y-3">
      <span className="sr-only">{label}…</span>
      {Array.from({ length: Math.max(1, rows) }, (_, index) => (
        <Skeleton className="h-12 w-full" key={index} />
      ))}
    </section>
  );
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="rounded-lg border border-dashed bg-background px-6 py-10 text-center">
      <h2 className="font-medium">{title}</h2>
      {description ? (
        <p className="mx-auto mt-2 max-w-lg text-sm text-muted-foreground">{description}</p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

export function ErrorState({
  title = "Something went wrong",
  message,
  requestId,
  onRetry,
  retryLabel = "Try again",
}: {
  title?: string;
  message: string;
  requestId?: string;
  onRetry?: () => void;
  retryLabel?: string;
}) {
  return (
    <section
      aria-live="polite"
      className="rounded-lg border border-destructive/30 bg-destructive/5 p-4"
      role="alert"
    >
      <div className="flex gap-3">
        <AlertCircle aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-destructive" />
        <div className="min-w-0">
          <h2 className="font-medium">{title}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{message}</p>
          {requestId ? (
            <p className="mt-2 text-xs text-muted-foreground">Request ID: {requestId}</p>
          ) : null}
          {onRetry ? (
            <Button className="mt-3" onClick={onRetry} size="sm" variant="outline">
              <RefreshCw aria-hidden="true" />
              {retryLabel}
            </Button>
          ) : null}
        </div>
      </div>
    </section>
  );
}
