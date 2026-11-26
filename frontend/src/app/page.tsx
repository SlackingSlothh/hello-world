import { Suspense } from "react";
import { Database, History, ShieldCheck, Workflow } from "lucide-react";
import { cacheLife } from "next/cache";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { PublicLayout } from "@/components/shared/layouts";
import { PageHeader } from "@/components/shared/page-header";
import { RootAuthActions } from "@/components/auth/root-auth-actions";

const features = [
  {
    title: "Browse databases",
    description:
      "Explore schemas, tables, columns, constraints, and paginated data.",
    icon: Database,
  },
  {
    title: "Read-only queries",
    description: "Run safe queries and review bounded, paginated results.",
    icon: History,
  },
  {
    title: "Migration workflow",
    description:
      "Draft changes, request approvals, and monitor asynchronous jobs.",
    icon: Workflow,
  },
  {
    title: "Group-based access",
    description: "Give users view or edit access through groups and leaders.",
    icon: ShieldCheck,
  },
];

async function LandingContent() {
  "use cache";
  cacheLife({ revalidate: 3600, expire: 86400 });

  return (
    <>
      <section className="sm:py-1">
        <PageHeader
          description="A collaborative workspace for browsing databases and managing reviewed schema migrations."
          title="HelloWorld!"
        />
      </section>
      <section aria-label="Features" className="grid gap-4 sm:grid-cols-2">
        {features.map(({ title, description, icon: Icon }) => (
          <Card key={title}>
            <CardHeader>
              <span className="mb-2 inline-flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
                <Icon aria-hidden="true" className="size-5" />
              </span>
              <CardTitle>{title}</CardTitle>
              <CardDescription>{description}</CardDescription>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              Built around explicit permissions and a reviewable change history.
            </CardContent>
          </Card>
        ))}
      </section>
    </>
  );
}

export default function HomePage() {
  return (
    <PublicLayout
      authActions={
        <Suspense fallback={<span aria-hidden="true" className="h-9 w-20" />}>
          <RootAuthActions placement="header" />
        </Suspense>
      }
    >
      <LandingContent />
      <div className="mt-4 flex flex-wrap gap-3">
        <Suspense fallback={<span aria-hidden="true" className="h-10 w-32" />}>
          <RootAuthActions placement="hero" />
        </Suspense>
      </div>
    </PublicLayout>
  );
}
