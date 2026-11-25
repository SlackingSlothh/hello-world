"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronDown } from "lucide-react";
import { toast } from "sonner";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { logout } from "@/lib/api/auth";

export function AccountMenu({
  username,
  isAdmin,
  mode,
}: {
  username: string;
  isAdmin: boolean;
  mode: "user" | "admin";
}) {
  const router = useRouter();
  const [signingOut, setSigningOut] = useState(false);

  async function signOut() {
    setSigningOut(true);
    try {
      await logout();
      window.location.replace("/login");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not sign out.",
      );
      setSigningOut(false);
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={`Account menu for ${username}`}
        className="inline-flex h-9 max-w-56 items-center gap-2 rounded-md px-3 text-sm font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className="truncate">{username}</span>
        <ChevronDown aria-hidden="true" className="size-4 shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        {isAdmin ? (
          <>
            <DropdownMenuItem
              onClick={() =>
                router.push(mode === "admin" ? "/db" : "/admin")
              }
            >
              Switch to {mode === "admin" ? "user" : "admin"} mode
            </DropdownMenuItem>
            <DropdownMenuSeparator />
          </>
        ) : null}
        <DropdownMenuItem disabled={signingOut} onClick={signOut}>
          {signingOut ? "Signing out…" : "Sign out"}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
