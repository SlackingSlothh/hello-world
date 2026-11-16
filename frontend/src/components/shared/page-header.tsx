import Link from "next/link";
import type { ReactNode } from "react";

export type BreadcrumbItem = {
  label: string;
  href?: string;
};

export function Breadcrumbs({ items }: { items: BreadcrumbItem[] }) {
  return (
    <nav aria-label="Breadcrumb" className="mb-3 text-sm text-muted-foreground">
      <ol className="flex flex-wrap items-center gap-2">
        {items.map((item, index) => (
          <li
            className="flex items-center gap-2"
            key={`${item.label}-${index}`}
          >
            {index > 0 ? <span aria-hidden="true">/</span> : null}
            {item.href && index < items.length - 1 ? (
              <Link className="hover:text-foreground" href={item.href}>
                {item.label}
              </Link>
            ) : (
              <span
                aria-current={index === items.length - 1 ? "page" : undefined}
              >
                {item.label}
              </span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}

export function PageHeader({
  title,
  description,
  breadcrumbs,
  actions,
}: {
  title: string;
  description?: string;
  breadcrumbs?: BreadcrumbItem[];
  actions?: ReactNode;
}) {
  return (
    <header className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div>
        {breadcrumbs?.length ? <Breadcrumbs items={breadcrumbs} /> : null}
        <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
          {title}
        </h1>
        {description ? (
          <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex flex-wrap items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}
