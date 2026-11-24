"use client";

import { useTransition } from "react";
import { useRouter } from "next/navigation";
import { AdminModeSwitcher } from "@/components/admin/admin-panels";

export function ModeSwitchControl({ mode }: { mode: "user" | "admin" }) {
  const router = useRouter();
  const [isPending, startTransition] = useTransition();
  const destination = mode === "admin" ? "/db" : "/admin";

  return (
    <AdminModeSwitcher
      busy={isPending}
      mode={mode}
      onSwitch={() => {
        startTransition(() => router.push(destination));
      }}
    />
  );
}
