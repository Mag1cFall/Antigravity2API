import type { ReactNode } from 'react'

// FormRow 是设置列表的一行：行宽不小于 720px 时左侧名称与说明、右侧控件，更窄时控件在说明下方，行间以细线分隔
export function FormRow({
  id,
  label,
  hint,
  tag,
  error,
  children,
}: {
  id: string
  label: ReactNode
  hint?: ReactNode
  tag?: ReactNode
  error?: string | undefined
  children: ReactNode
}) {
  return (
    <div className="@container border-t border-border py-4 first:border-t-0">
      <div className="flex flex-col gap-3 @min-[720px]:flex-row @min-[720px]:items-start @min-[720px]:justify-between @min-[720px]:gap-8">
        <div className="flex min-w-0 flex-col gap-1 @min-[720px]:max-w-prose">
          <div className="flex flex-wrap items-center gap-2">
            <label htmlFor={id} className="text-base font-medium">
              {label}
            </label>
            {tag}
          </div>
          {hint && (
            <p id={id + '-hint'} className="text-sm text-foreground-muted">
              {hint}
            </p>
          )}
        </div>
        <div className="flex w-full shrink-0 flex-col gap-2 @min-[720px]:w-field">
          {children}
          {error && (
            <p id={id + '-error'} role="alert" className="text-sm text-error-text">
              {error}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
