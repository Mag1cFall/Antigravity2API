import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Boxes,
  Cable,
  Copy,
  History,
  LayoutDashboard,
  LogOut,
  type LucideIcon,
  Minus,
  Moon,
  Settings,
  Square,
  Sun,
  Users,
  X,
} from 'lucide-react'
import { type ReactNode, useEffect, useState, useSyncExternalStore } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router'

import { appMessages } from '@/app/messages'
import { lastLocation } from '@/app/remember'
import logo from '@/assets/logo.png'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Indicator } from '@/components/ui/indicator'
import { Tooltip } from '@/components/ui/tooltip'
import { sessionKey, useSession } from '@/features/service/queries'
import { api } from '@/lib/api'
import { isDesktop, maximisedEvent, windowControl } from '@/lib/desktop'
import { type Language, languages, useI18n, useMessages } from '@/lib/i18n'
import { currentTheme, subscribeTheme, toggleTheme } from '@/lib/theme'

// NavItem 是一个导航目的地
interface NavItem {
  to: string
  label: string
  icon: LucideIcon
}

// roundButton 是顶栏右侧圆形按钮的外观
const roundButton =
  'size-control rounded-full border-card-border bg-surface text-foreground shadow-card no-drag enabled:hover:border-border enabled:hover:bg-muted enabled:hover:text-foreground enabled:active:bg-hover'

// MainNav 是居中的等宽胶囊导航，当前页的深色胶囊滑到该项，各项回到该页上次的子页面与查询参数；compact 用于窄屏顶栏下方每行三项的两行
function MainNav({ items, label, compact }: { items: NavItem[]; label: string; compact: boolean }) {
  const location = useLocation()
  return (
    <nav
      aria-label={label}
      className={'relative mx-auto ' + (compact ? 'w-full' : 'w-fit min-w-0')}
    >
      <Indicator active="[aria-current=page]" className="rounded-full bg-foreground" />
      <ul
        className={
          'grid gap-1 border border-card-border bg-surface p-1 shadow-card ' +
          (compact ? 'grid-cols-3 rounded-[20px]' : 'w-max auto-cols-fr grid-flow-col rounded-full')
        }
      >
        {items.map((item) => (
          <li key={item.to} className="flex">
            <NavLink
              to={lastLocation(item.to, item.to === '/', location)}
              end={item.to === '/'}
              className={
                'relative flex h-control-sm w-full items-center justify-center gap-2 rounded-full text-sm font-medium whitespace-nowrap text-foreground-muted transition-colors duration-200 ease-out no-drag hover:text-foreground not-aria-[current=page]:hover:bg-muted aria-[current=page]:text-surface pointer-coarse:h-touch ' +
                (compact ? 'px-2' : 'px-4 max-lg:px-3')
              }
            >
              <item.icon
                size={16}
                aria-hidden
                className={'shrink-0 ' + (compact ? '' : 'max-xl:hidden')}
              />
              {item.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}

// WindowButton 是桌面无边框窗口的窗口按钮
function WindowButton({
  label,
  onClick,
  children,
}: {
  label: string
  onClick: () => void
  children: ReactNode
}) {
  return (
    <Tooltip label={label}>
      <Button
        variant="ghost"
        size="icon"
        aria-label={label}
        onClick={onClick}
        className="text-foreground no-drag enabled:hover:bg-header-hover enabled:active:bg-muted"
      >
        {children}
      </Button>
    </Tooltip>
  )
}

// WindowControls 是桌面无边框窗口右上角的最小化、最大化与关闭
function WindowControls() {
  const m = useMessages(appMessages)
  const [maximised, setMaximised] = useState(false)
  useEffect(() => {
    const onChange = (event: Event) => {
      setMaximised((event as CustomEvent<boolean>).detail)
    }
    window.addEventListener(maximisedEvent, onChange)
    return () => {
      window.removeEventListener(maximisedEvent, onChange)
    }
  }, [])
  return (
    <div className="ml-1 flex items-center gap-1 border-l border-border pl-3">
      <WindowButton
        label={m.minimise}
        onClick={() => {
          windowControl('minimise')
        }}
      >
        <Minus size={20} aria-hidden />
      </WindowButton>
      <WindowButton
        label={maximised ? m.restore : m.maximise}
        onClick={() => {
          windowControl('toggle-maximise')
        }}
      >
        {maximised ? <Copy size={18} aria-hidden /> : <Square size={18} aria-hidden />}
      </WindowButton>
      <WindowButton
        label={m.close}
        onClick={() => {
          windowControl('close')
        }}
      >
        <X size={20} aria-hidden />
      </WindowButton>
    </div>
  )
}

// Header 是顶栏：左侧标志，居中胶囊导航，右侧语言与配色，桌面窗口中整条可拖动并带窗口按钮；minimal 时不显示导航
export function Header({ minimal = false }: { minimal?: boolean }) {
  const m = useMessages(appMessages)
  const { language, setLanguage } = useI18n()
  const theme = useSyncExternalStore(subscribeTheme, currentTheme)
  const client = useQueryClient()
  const navigate = useNavigate()
  const session = useSession()
  const logout = useMutation({
    mutationFn: () => api('/api/session', { method: 'DELETE' }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: sessionKey })
      void navigate('/login')
    },
  })
  const canLogout = session.data?.enabled === true && session.data.authenticated
  const items: NavItem[] = [
    { to: '/', label: m.dashboard, icon: LayoutDashboard },
    { to: '/accounts', label: m.accounts, icon: Users },
    { to: '/connect', label: m.connect, icon: Cable },
    { to: '/models', label: m.models, icon: Boxes },
    { to: '/requests', label: m.requests, icon: History },
    { to: '/settings', label: m.settings, icon: Settings },
  ]
  const themeLabel = theme === 'dark' ? m.themeToLight : m.themeToDark
  return (
    <header className={'shrink-0 select-none ' + (isDesktop ? 'drag-region' : '')}>
      <div
        className={
          'mx-auto grid h-header w-full grid-cols-[1fr_auto_1fr] items-center gap-4 px-6 max-sm:px-4 ' +
          (isDesktop ? '' : 'max-w-page')
        }
      >
        <Link
          to="/"
          className="flex min-w-0 items-center gap-3 justify-self-start rounded-md no-drag"
        >
          <img src={logo} alt="" width={32} height={32} className="size-8 shrink-0" />
          <span className="truncate text-md font-semibold max-xl:hidden">{m.product}</span>
        </Link>
        {minimal ? (
          <span />
        ) : (
          <div className="max-md:hidden">
            <MainNav items={items} label={m.navLabel} compact={false} />
          </div>
        )}
        <div className="col-start-3 flex items-center justify-end gap-2">
          <DropdownMenu>
            <Tooltip label={m.language}>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="secondary"
                  size="icon"
                  aria-label={m.language}
                  className={roundButton + ' text-xs font-semibold'}
                >
                  {languages.find((item) => item.value === language)?.short}
                </Button>
              </DropdownMenuTrigger>
            </Tooltip>
            <DropdownMenuContent>
              <DropdownMenuRadioGroup
                value={language}
                onValueChange={(value) => {
                  setLanguage(value as Language)
                }}
              >
                {languages.map((item) => (
                  <DropdownMenuRadioItem key={item.value} value={item.value}>
                    {item.name}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
          <Tooltip label={themeLabel}>
            <Button
              variant="secondary"
              size="icon"
              aria-label={themeLabel}
              data-theme-current={theme}
              className={roundButton + ' group/theme relative overflow-hidden'}
              onClick={(event) => {
                const rect = event.currentTarget.getBoundingClientRect()
                toggleTheme({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
              }}
            >
              <Sun
                size={18}
                aria-hidden
                className="absolute transition-[transform,opacity] duration-300 ease-out group-data-[theme-current=dark]/theme:scale-50 group-data-[theme-current=dark]/theme:rotate-90 group-data-[theme-current=dark]/theme:opacity-0"
              />
              <Moon
                size={18}
                aria-hidden
                className="absolute transition-[transform,opacity] duration-300 ease-out group-data-[theme-current=light]/theme:scale-50 group-data-[theme-current=light]/theme:-rotate-90 group-data-[theme-current=light]/theme:opacity-0"
              />
            </Button>
          </Tooltip>
          {canLogout && (
            <Tooltip label={m.logout}>
              <Button
                variant="secondary"
                size="icon"
                aria-label={m.logout}
                className={roundButton}
                onClick={() => {
                  logout.mutate()
                }}
              >
                <LogOut size={18} aria-hidden />
              </Button>
            </Tooltip>
          )}
          {isDesktop && <WindowControls />}
        </div>
      </div>
      {!minimal && (
        <div className="px-4 pb-3 md:hidden">
          <MainNav items={items} label={m.navLabel} compact />
        </div>
      )}
    </header>
  )
}
