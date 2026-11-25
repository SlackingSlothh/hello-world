import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { headers } from "next/headers";
import { AccountMenu } from "@/components/auth/account-menu";

type RootAuthActionsProps = {
  placement: "header" | "hero";
};

export async function RootAuthActions({
  placement,
}: RootAuthActionsProps) {
  const requestHeaders = await headers();
  const state = requestHeaders.get("x-datadock-auth-state");

  if (state === "unavailable") {
    return placement === "header" ? (
      <span className="text-sm text-muted-foreground" role="status">
        Account status unavailable
      </span>
    ) : (
      <span className="text-sm text-muted-foreground" role="status">
        Unable to verify sign-in status.
      </span>
    );
  }

  if (state !== "authenticated") {
    return placement === "header" ? (
      <Link
        className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        href="/login"
      >
        Sign in
      </Link>
    ) : (
      <Link
        className="inline-flex h-10 items-center justify-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        href="/login"
      >
        Login <ArrowRight aria-hidden="true" />
      </Link>
    );
  }

  const username = requestHeaders.get("x-datadock-username");
  if (!username) {
    return (
      <span className="text-sm text-muted-foreground" role="status">
        Account status unavailable
      </span>
    );
  }

  if (placement === "header") {
    return (
      <AccountMenu
        isAdmin={requestHeaders.get("x-datadock-admin") === "true"}
        mode="user"
        username={username}
      />
    );
  }

  return (
    <Link
      className="inline-flex h-10 items-center justify-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      href="/db"
    >
      Go to databases <ArrowRight aria-hidden="true" />
    </Link>
  );
}

export async function RootUserNavigation() {
  const requestHeaders = await headers();
  if (requestHeaders.get("x-datadock-auth-state") !== "authenticated") {
    return null;
  }

  return (
    <>
      <Link
        className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        href="/db"
      >
        Databases
      </Link>
      <Link
        className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        href="/migration"
      >
        Migrations
      </Link>
    </>
  );
}
