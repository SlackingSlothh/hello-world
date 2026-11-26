import { Suspense } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { PublicLayout } from "@/components/shared/layouts";
import {
  RootAuthActions,
  RootUserNavigation,
} from "@/components/auth/root-auth-actions";

export default function NotFoundPage() {
  return (
    <PublicLayout
      authNavigation={
        <Suspense fallback={null}>
          <RootUserNavigation />
        </Suspense>
      }
      authActions={
        <Suspense fallback={<span aria-hidden="true" className="h-9 w-20" />}>
          <RootAuthActions placement="header" />
        </Suspense>
      }
    >
      <section className="mx-auto max-w-xl py-16 text-center">
        <p className="text-sm font-semibold uppercase tracking-widest text-primary">
          404
        </p>
        <h1 className="mt-3 text-3xl font-semibold tracking-tight">
          Page not found
        </h1>
        <p className="mt-3 text-sm text-muted-foreground">
          The page or database you requested could not be found.
        </p>
        <Button className="mt-6" render={<Link href="/" />}>
          Back to home
        </Button>
      </section>
    </PublicLayout>
  );
}
