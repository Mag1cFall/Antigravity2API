import type { ComponentProps, ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

// Table 是数据表，卡片内铺满卡片宽度、首尾列与卡片标题对齐；framed 时自带边框，用于对话框内；过宽时只在表格内横向滚动
export function Table({
  label,
  framed = false,
  fixed = false,
  minWidth,
  children,
}: {
  label: string
  framed?: boolean
  fixed?: boolean
  minWidth?: number
  children: ReactNode
}) {
  return (
    <div
      className={twMerge(
        'relative max-w-full overflow-x-auto',
        framed
          ? 'rounded-md border border-border [--edge:16px]'
          : '[--edge:24px] max-sm:[--edge:16px]',
      )}
    >
      <table
        aria-label={label}
        style={minWidth ? { minWidth } : undefined}
        className={twMerge(
          'w-full border-collapse text-sm [&>tbody>tr]:transition-colors [&>tbody>tr]:hover:bg-muted/60',
          fixed && 'table-fixed',
        )}
      >
        {children}
      </table>
    </div>
  )
}

// Th 是表头单元格
export function Th({ className, ...props }: ComponentProps<'th'>) {
  return (
    <th
      scope="col"
      className={twMerge(
        'h-control bg-muted px-3 text-left text-xs font-medium whitespace-nowrap text-foreground-muted first:pl-(--edge) last:pr-(--edge)',
        className,
      )}
      {...props}
    />
  )
}

// Td 是数据单元格，数字列用等宽数字
export function Td({ className, ...props }: ComponentProps<'td'>) {
  return (
    <td
      className={twMerge(
        'border-t border-border px-3 py-3 align-middle tabular-nums first:pl-(--edge) last:pr-(--edge)',
        className,
      )}
      {...props}
    />
  )
}
