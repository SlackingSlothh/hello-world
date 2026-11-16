import Link from "next/link";
import { PublicLayout } from "@/components/shared/layouts";
import { AuthPageForm } from "@/components/pages/page-clients";

export default function RegisterPage() {
  return (
    <PublicLayout>
      <div className="space-y-4 py-8">
        <AuthPageForm mode="register" />
        <p className="text-center text-sm text-muted-foreground">
          Already registered?{" "}
          <Link className="text-primary underline" href="/login">
            Sign in
          </Link>
        </p>
      </div>
    </PublicLayout>
  );
}
