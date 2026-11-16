"use client";

import { Button } from "@/components/ui/button";

export function PaginationControls({
  page,
  pageSize,
  total,
  onPageChange,
  disabled = false,
}: {
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  disabled?: boolean;
}) {
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.min(Math.max(1, page), pageCount);
  const firstItem = total === 0 ? 0 : (safePage - 1) * pageSize + 1;
  const lastItem = Math.min(safePage * pageSize, total);

  return (
    <nav aria-label="Pagination" className="flex flex-wrap items-center justify-between gap-3">
      <p className="text-sm text-muted-foreground">
        Showing {firstItem}–{lastItem} of {total}
      </p>
      <div className="flex items-center gap-2">
        <Button
          disabled={disabled || safePage <= 1}
          onClick={() => onPageChange(safePage - 1)}
          size="sm"
          variant="outline"
        >
          Previous
        </Button>
        <span aria-current="page" className="min-w-16 text-center text-sm">
          Page {safePage} of {pageCount}
        </span>
        <Button
          disabled={disabled || safePage >= pageCount}
          onClick={() => onPageChange(safePage + 1)}
          size="sm"
          variant="outline"
        >
          Next
        </Button>
      </div>
    </nav>
  );
}
