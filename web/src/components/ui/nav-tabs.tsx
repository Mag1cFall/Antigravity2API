import type { LucideIcon } from 'lucide-react'
import { NavLink } from 'react-router'

import { Indicator } from '@/components/ui/indicator'

// NavTab 是一个页内导航项
export interface NavTab {
  to: string
  label: string
  icon: LucideIcon
  end?: boolean
}

// NavTabs 是页内子页面导航，各项等宽，当前项的深色背景滑到该项
export function NavTabs({ items, label }: { items: NavTab[]; label: string }) {
  return (
    <nav aria-label={label} className="relative w-fit max-w-full overflow-x-auto">
      <Indicator active="[aria-current=page]" className="rounded-md bg-foreground" />
      <ul className="grid w-fit auto-cols-fr grid-flow-col gap-1 rounded-lg bg-surface p-1 shadow-card">
        {items.map((item) => (
          <li key={item.to} className="flex">
            <NavLink
              to={item.to}
              end={item.end ?? false}
              className="relative flex h-control-sm w-full items-center justify-center gap-2 rounded-md px-4 text-sm whitespace-nowrap text-foreground-muted transition-colors duration-200 ease-out hover:text-foreground not-aria-[current=page]:hover:bg-muted aria-[current=page]:text-surface pointer-coarse:h-touch"
            >
              <item.icon size={16} aria-hidden />
              {item.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}
