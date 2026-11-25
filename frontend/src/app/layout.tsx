import type { Metadata } from "next";
import { Geist, Geist_Mono, Inter } from "next/font/google";
import "./globals.css";
import { cn } from "@/lib/utils";
import { ToastRegion } from "@/components/shared/toast-region";

const inter = Inter({ subsets: ["latin"], variable: "--font-sans" });

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: {
    default: "DataDock",
    template: "%s · DataDock",
  },
  description: "Browse databases and manage reviewed schema migrations.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="en"
      className={cn(
        "h-full",
        "antialiased",
        geistSans.variable,
        geistMono.variable,
        "font-sans",
        inter.variable,
      )}
    >
      <body className="min-h-full flex flex-col">
        <div className="border-b border-amber-300 bg-amber-50 px-4 py-2 text-center text-xs text-amber-950">
          Authentication uses the backend. Database, admin, and migration
          features still use demo data.
        </div>
        {children}
        <ToastRegion />
      </body>
    </html>
  );
}
