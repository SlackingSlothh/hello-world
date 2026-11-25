import Link from "next/link";
import type { ReactNode } from "react";
import { AccountMenu } from "@/components/auth/account-menu";

type LayoutProps = {
  children: ReactNode;
};

function Shell({
  children,
  navigation,
  identity,
}: LayoutProps & {
  navigation: ReactNode;
  identity?: ReactNode;
}) {
  return (
    <div className="min-h-screen bg-muted/30">
      <header className="border-b bg-background">
        <div className="mx-auto flex min-h-16 max-w-7xl flex-wrap items-center justify-between gap-4 px-4 py-3 sm:px-6 lg:px-8">
          <Link className="text-lg font-semibold tracking-tight" href="/">
            HelloWorld
          </Link>
          <nav
            aria-label="Main navigation"
            className="flex flex-wrap items-center gap-2"
          >
            {navigation}
          </nav>
          {identity}
        </div>
      </header>
      <main className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 sm:py-8 lg:px-8">
        {children}
      </main>
    </div>
  );
}

function NavLink({ href, children }: LayoutProps & { href: string }) {
  return (
    <Link
      className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      href={href}
    >
      {children}
    </Link>
  );
}

export function PublicLayout({ children }: LayoutProps) {
  return (
    <Shell
      navigation={<NavLink href="/login">Sign in</NavLink>}
    >
      {children}
    </Shell>
  );
}

export function AuthenticatedLayout({
  children,
  username,
  isAdmin = false,
}: LayoutProps & { username: string; isAdmin: boolean }) {
  return (
    <Shell
      identity={<AccountMenu isAdmin={isAdmin} mode="user" username={username} />}
      navigation={
        <>
          <NavLink href="/db">Databases</NavLink>
          <NavLink href="/migration">Migrations</NavLink>
        </>
      }
    >
      {children}
    </Shell>
  );
}

export function AdminLayout({
  children,
  username,
}: LayoutProps & { username: string }) {
  return (
    <Shell
      identity={<AccountMenu isAdmin mode="admin" username={username} />}
      navigation={
        <>
          <NavLink href="/admin/connections">Connections</NavLink>
          <NavLink href="/admin/groups">Groups</NavLink>
          <NavLink href="/admin/access">Access</NavLink>
          <NavLink href="/migration">Migrations</NavLink>
        </>
      }
    >
      {children}
    </Shell>
  );
}
