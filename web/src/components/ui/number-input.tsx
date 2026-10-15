import { Minus, Plus } from 'lucide-react'
import type { ComponentProps } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tooltip } from '@/components/ui/tooltip'

// NumberInput 是带加减按钮的整数输入框，替代浏览器自带的上下箭头；到达上下限时加减按钮提示 minReason 与 maxReason
export function NumberInput({
  value,
  onChange,
  min,
  max,
  decreaseLabel,
  increaseLabel,
  minReason,
  maxReason,
  ...props
}: Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'min' | 'max'> & {
  value: number
  onChange: (value: number) => void
  min: number
  max: number
  decreaseLabel: string
  increaseLabel: string
  minReason: string
  maxReason: string
}) {
  const clamp = (next: number) => Math.min(max, Math.max(min, next))
  const atMin = !props.disabled && value <= min
  const atMax = !props.disabled && value >= max
  return (
    <div className="flex items-center gap-1">
      <Tooltip disabledReason={atMin && minReason}>
        <Button
          size="icon"
          aria-label={decreaseLabel}
          disabled={props.disabled === true || value <= min}
          onClick={() => {
            onChange(clamp(value - 1))
          }}
        >
          <Minus size={16} aria-hidden />
        </Button>
      </Tooltip>
      <Input
        inputMode="numeric"
        value={Number.isNaN(value) ? '' : String(value)}
        onChange={(event) => {
          const digits = event.target.value.replace(/\D/g, '')
          onChange(digits === '' ? Number.NaN : Number(digits))
        }}
        className="w-16 text-center tabular-nums"
        {...props}
      />
      <Tooltip disabledReason={atMax && maxReason}>
        <Button
          size="icon"
          aria-label={increaseLabel}
          disabled={props.disabled === true || value >= max}
          onClick={() => {
            onChange(clamp(value + 1))
          }}
        >
          <Plus size={16} aria-hidden />
        </Button>
      </Tooltip>
    </div>
  )
}
