import { Suspense } from "react";
import Link from "next/link";
import { PublicLayout } from "@/components/shared/layouts";
import { AuthPageForm } from "@/components/pages/page-clients";
import { LoadingState } from "@/components/shared/async-states";

type LoginPageProps = {
  searchParams: Promise<{ next?: string; returnTo?: string; error?: string }>;
};

export default function LoginPage(props: LoginPageProps) {
  return (
    <PublicLayout>
      <Suspense fallback={<LoadingState label="Loading sign-in form" rows={2} />}>
        <LoginPageContent {...props} />
      </Suspense>
    </PublicLayout>
  );
}

async function LoginPageContent({ searchParams }: LoginPageProps) {
  const params = await searchParams;
  return (
    <div className="space-y-4 py-8">
      <AuthPageForm
        mode="login"
        nextPath={params.next ?? params.returnTo}
        sessionExpired={params.error === "SESSION_EXPIRED"}
      />
      <p className="text-center text-sm text-muted-foreground">
        New here? <Link className="text-primary underline" href="/register">Create an account</Link>
      </p>
    </div>
  );
}
