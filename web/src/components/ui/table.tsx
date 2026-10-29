import type { ComponentProps, CSSProperties, ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

// stackClass 是窄屏时把每行排成一张卡片的样式：单元格依次换行排列，表头只保留带 data-stack 的单元格，没有这样的单元格时整行隐藏；行的样式不带优先级，行上的类可以替换
const stackClass =
  'max-sm:block max-sm:min-w-0 max-sm:[&>colgroup]:hidden max-sm:[&>tbody]:block max-sm:[&>thead]:block max-sm:[&>thead:not(:has([data-stack]))]:hidden max-sm:[&>thead:not(:has([data-stack]))+tbody>tr:first-child]:border-t-0 max-sm:[&>thead>tr]:flex max-sm:[&>thead>tr]:bg-muted max-sm:[&>thead>tr]:px-4 max-sm:[&>thead>tr>th:not([data-stack])]:hidden max-sm:[:where(&>tbody>tr)]:flex max-sm:[:where(&>tbody>tr)]:flex-wrap max-sm:[:where(&>tbody>tr)]:items-start max-sm:[:where(&>tbody>tr)]:gap-x-4 max-sm:[:where(&>tbody>tr)]:gap-y-3 max-sm:[:where(&>tbody>tr)]:border-t max-sm:[:where(&>tbody>tr)]:border-border max-sm:[:where(&>tbody>tr)]:px-4 max-sm:[:where(&>tbody>tr)]:py-3'

// stackThird 与 stackTwoThirds 是窄屏卡片中占一栏与两栏的单元格宽度，卡片内每行三栏
export const stackThird = 'max-sm:w-[calc((100%-32px)/3)]'
export const stackTwoThirds = 'max-sm:w-[calc((100%-32px)/3*2+16px)]'

// Table 是数据表，卡片内铺满卡片宽度、首尾列与卡片标题对齐；framed 时自带边框，用于对话框内；过宽时只在表格内横向滚动；stack 时宽度小于 640px 改为每行一张卡片
export function Table({
  label,
  framed = false,
  fixed = false,
  stack = false,
  minWidth,
  children,
}: {
  label: string
  framed?: boolean
  fixed?: boolean
  stack?: boolean
  minWidth?: number
  children: ReactNode
}) {
  return (
    <div
      className={twMerge(
        'relative max-w-full overflow-x-auto',
        framed ? 'rounded-md border border-border [--edge:12px]' : '[--edge:16px]',
        stack && 'max-sm:[--edge:0px]',
      )}
    >
      <table
        aria-label={label}
        data-stack={stack || undefined}
        style={minWidth ? ({ '--table-min': String(minWidth) + 'px' } as CSSProperties) : undefined}
        className={twMerge(
          'w-full border-collapse text-sm [&>tbody>tr]:transition-colors [&>tbody>tr]:hover:bg-muted/60',
          minWidth && 'min-w-(--table-min)',
          fixed && 'table-fixed',
          stack && stackClass,
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
        'h-control-sm bg-muted px-3 text-left text-xs font-medium whitespace-nowrap text-foreground-muted first:pl-(--edge) last:pr-(--edge)',
        className,
      )}
      {...props}
    />
  )
}

// Td 是数据单元格，数字列用等宽数字；label 是窄屏卡片中显示在内容上方的列名
export function Td({
  className,
  label,
  children,
  ...props
}: ComponentProps<'td'> & { label?: string }) {
  return (
    <td
      className={twMerge(
        'border-t border-border px-3 py-3 align-middle tabular-nums first:pl-(--edge) last:pr-(--edge) max-sm:in-data-stack:min-w-0 max-sm:in-data-stack:border-0 max-sm:in-data-stack:p-0',
        className,
      )}
      {...props}
    >
      {label && (
        <span className="mb-1 hidden text-xs font-normal text-foreground-muted max-sm:in-data-stack:block">
          {label}
        </span>
      )}
      {children}
    </td>
  )
}
