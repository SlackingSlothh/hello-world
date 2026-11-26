import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { cache } from "react";
import { headers } from "next/headers";
import { AccountMenu } from "@/components/auth/account-menu";
import { backendAuthUrl } from "@/lib/server/backend-auth";

type RootAuthActionsProps = {
  placement: "header" | "hero";
};

type RootAuthState = {
  state: "anonymous" | "authenticated" | "unavailable";
  username?: string;
  isAdmin: boolean;
};

const getRootAuthState = cache(async (): Promise<RootAuthState> => {
  const requestHeaders = await headers();
  const forwardedState = requestHeaders.get("x-datadock-auth-state");
  if (
    forwardedState === "anonymous" ||
    forwardedState === "authenticated" ||
    forwardedState === "unavailable"
  ) {
    return {
      state: forwardedState,
      username: requestHeaders.get("x-datadock-username") ?? undefined,
      isAdmin: requestHeaders.get("x-datadock-admin") === "true",
    };
  }
  if (forwardedState !== null) {
    return { state: "unavailable", isAdmin: false };
  }

  const cookie = requestHeaders.get("cookie");
  if (!cookie?.split(";").some((part) => part.trim().startsWith("session_id="))) {
    return { state: "anonymous", isAdmin: false };
  }

  let sessionResponse: Response;
  try {
    const request = {
      headers: { cookie },
      cache: "no-store" as const,
      redirect: "manual" as const,
    };
    sessionResponse = await fetch(backendAuthUrl("session"), request);
    if (sessionResponse.status === 401) {
      return { state: "anonymous", isAdmin: false };
    }
    if (!sessionResponse.ok) {
      return { state: "unavailable", isAdmin: false };
    }

    const profile: unknown = await sessionResponse.json();
    if (
      typeof profile !== "object" ||
      profile === null ||
      !("username" in profile) ||
      typeof profile.username !== "string" ||
      profile.username.length === 0
    ) {
      return { state: "unavailable", isAdmin: false };
    }

    const adminResponse = await fetch(backendAuthUrl("admin-session"), request);
    if (adminResponse.status === 401) {
      return { state: "anonymous", isAdmin: false };
    }
    if (adminResponse.status !== 204 && adminResponse.status !== 403) {
      return { state: "unavailable", isAdmin: false };
    }
    return {
      state: "authenticated",
      username: profile.username,
      isAdmin: adminResponse.status === 204,
    };
  } catch (error) {
    if (error instanceof TypeError || error instanceof SyntaxError) {
      return { state: "unavailable", isAdmin: false };
    }
    throw error;
  }
});

export async function RootAuthActions({
  placement,
}: RootAuthActionsProps) {
  const { state, username, isAdmin } = await getRootAuthState();

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
        isAdmin={isAdmin}
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
  const { state } = await getRootAuthState();
  if (state !== "authenticated") {
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
