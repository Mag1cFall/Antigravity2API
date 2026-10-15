import { ChevronLeft, ChevronRight } from 'lucide-react'

import { Button } from '@/components/ui/button'

// pageItems 返回要显示的页码，首尾与当前页附近之外用省略号代替
function pageItems(page: number, pages: number): (number | 'gap')[] {
  const shown = new Set([1, pages, page - 1, page, page + 1].filter((n) => n >= 1 && n <= pages))
  const items: (number | 'gap')[] = []
  let last = 0
  for (const n of [...shown].sort((a, b) => a - b)) {
    if (n - last > 1) items.push('gap')
    items.push(n)
    last = n
  }
  return items
}

// Pagination 是表格底部的条数说明与页码，page 从 1 开始
export function Pagination({
  page,
  pages,
  summary,
  label,
  previousLabel,
  nextLabel,
  pageLabel,
  onPage,
}: {
  page: number
  pages: number
  summary: string
  label: string
  previousLabel: string
  nextLabel: string
  pageLabel: (page: number) => string
  onPage: (page: number) => void
}) {
  return (
    <nav
      aria-label={label}
      className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-6 py-3 max-sm:px-4"
    >
      <p className="text-sm text-foreground-muted tabular-nums">{summary}</p>
      {pages > 1 && (
        <ul className="flex items-center gap-1">
          <li>
            <Button
              variant="ghost"
              size="icon"
              className="size-control-sm"
              aria-label={previousLabel}
              disabled={page <= 1}
              onClick={() => {
                onPage(page - 1)
              }}
            >
              <ChevronLeft size={16} aria-hidden />
            </Button>
          </li>
          {pageItems(page, pages).map((item, index) =>
            item === 'gap' ? (
              <li key={'gap' + String(index)} aria-hidden className="w-6 text-center text-sm">
                …
              </li>
            ) : (
              <li key={item}>
                <Button
                  variant={item === page ? 'primary' : 'ghost'}
                  size="sm"
                  aria-label={pageLabel(item)}
                  aria-current={item === page ? 'page' : undefined}
                  className="min-w-control-sm px-2 tabular-nums"
                  onClick={() => {
                    onPage(item)
                  }}
                >
                  {item}
                </Button>
              </li>
            ),
          )}
          <li>
            <Button
              variant="ghost"
              size="icon"
              className="size-control-sm"
              aria-label={nextLabel}
              disabled={page >= pages}
              onClick={() => {
                onPage(page + 1)
              }}
            >
              <ChevronRight size={16} aria-hidden />
            </Button>
          </li>
        </ul>
      )}
    </nav>
  )
}
