import Link from "next/link";
import {
  ArrowRight,
  Database,
  History,
  ShieldCheck,
  Workflow,
} from "lucide-react";
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
      <section className="py-8 sm:py-14">
        <PageHeader
          description="A collaborative workspace for browsing databases and managing reviewed schema migrations."
          title="HelloWorld!"
        />
        <div className="flex flex-wrap gap-3">
          <Link
            className="inline-flex h-10 items-center justify-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            href="/login"
          >
            Login <ArrowRight aria-hidden="true" />
          </Link>
        </div>
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
    <PublicLayout>
      <LandingContent />
    </PublicLayout>
  );
}
