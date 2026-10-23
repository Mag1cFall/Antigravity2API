import { ToggleGroup } from 'radix-ui'

import { Indicator } from '@/components/ui/indicator'

// Segmented 是分段选择，用于筛选与范围切换，选中背景滑到选中项，放不下时换行
export function Segmented<T extends string>({
  value,
  onValueChange,
  options,
  label,
}: {
  value: T
  onValueChange: (value: T) => void
  options: { value: T; label: string }[]
  label: string
}) {
  return (
    <ToggleGroup.Root
      type="single"
      value={value}
      onValueChange={(next) => {
        if (next) onValueChange(next as T)
      }}
      aria-label={label}
      className="relative inline-flex w-fit max-w-full flex-wrap gap-1 rounded-lg bg-muted p-1"
    >
      <Indicator active="[data-state=on]" className="rounded-md bg-surface shadow-raised" />
      {options.map((option) => (
        <ToggleGroup.Item
          key={option.value}
          value={option.value}
          className="relative h-control-sm shrink-0 rounded-md px-3 text-sm whitespace-nowrap text-foreground-muted transition-colors duration-200 ease-out hover:text-foreground data-[state=off]:hover:bg-hover data-[state=on]:text-foreground pointer-coarse:h-touch"
        >
          {option.label}
        </ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  )
}
