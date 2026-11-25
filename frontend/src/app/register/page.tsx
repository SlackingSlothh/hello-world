import Link from "next/link";
import { PublicLayout } from "@/components/shared/layouts";

export default function RegisterPage() {
  return (
    <PublicLayout>
      <div className="mx-auto max-w-md space-y-4 py-8 text-center">
        <h1 className="text-2xl font-semibold tracking-tight">
          Account creation is managed by an administrator
        </h1>
        <p className="text-sm text-muted-foreground">
          Self-service registration is not available. Ask an administrator to
          create your account.
        </p>
        <Link className="text-primary underline" href="/login">
          Return to sign in
        </Link>
      </div>
    </PublicLayout>
  );
}
