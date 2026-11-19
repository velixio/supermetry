import { useParams, useRouter } from '@tanstack/react-router'
import {
  ActivityIcon, BanknoteIcon, FilterIcon, FlagIcon, GaugeIcon, LinkIcon, MousePointerClickIcon,
  RouteIcon, TimerResetIcon, UserRoundIcon, UsersRoundIcon, VideoIcon, ZapIcon,
} from 'lucide-react'
import type { ComponentType, SVGProps } from 'react'

import { EmptyState } from '@/components/EmptyState'
import { uiOnboarding } from '@/messages/uiOnboarding'

import { useSiteInstalled } from './useSiteInstalled'

export type EmptyKind = keyof Omit<typeof uiOnboarding.empty, 'docs'>

const icons: Record<EmptyKind, ComponentType<SVGProps<SVGSVGElement>>> = {
  events: ZapIcon, sessions: ActivityIcon, profiles: UserRoundIcon, cohorts: UsersRoundIcon, funnels: FilterIcon,
  goals: FlagIcon, journeys: RouteIcon, retention: TimerResetIcon, heatmaps: MousePointerClickIcon,
  replays: VideoIcon, revenue: BanknoteIcon, vitals: GaugeIcon, links: LinkIcon,
}

/** Where the primary action goes when the page does not supply its own handler. */
const settingsPage: Record<EmptyKind, string> = {
  events: 'installation', sessions: 'installation', profiles: 'installation', cohorts: 'installation',
  funnels: 'installation', goals: 'installation', journeys: 'installation', retention: 'installation',
  heatmaps: 'tracking', replays: 'replay', revenue: 'installation', vitals: 'tracking', links: 'installation',
}

export const docsHref = 'https://supermetry.com/docs'

/**
 * The empty state of an analytics page: what the page is for, one primary action and a docs link.
 * Pages that can create the thing in place pass `onAction`; the others link to the settings page
 * that turns the feature on.
 */
export function FeatureEmptyState(props: { kind: EmptyKind; onAction?: () => void; noAction?: boolean }) {
  // Rendered outside a router (a few unit tests), the settings link is simply left out.
  const router = useRouter({ warn: false })
  return router ? <RoutedEmptyState {...props} /> : <Content {...props} />
}

function RoutedEmptyState(props: { kind: EmptyKind; onAction?: () => void; noAction?: boolean }) {
  const { org, site } = useParams({ strict: false }) as { org?: string; site?: string }
  const base = org && site ? `/app/${org}/${site}` : undefined
  // Only the pages with "data is arriving" copy look up the install status.
  return 'installed' in uiOnboarding.empty[props.kind]
    ? <InstallAwareContent {...props} base={base} org={org} site={site} />
    : <Content {...props} base={base} />
}

function InstallAwareContent(props: { kind: EmptyKind; onAction?: () => void; noAction?: boolean; base?: string; org?: string; site?: string }) {
  const { org, site, ...rest } = props
  return <Content {...rest} installed={useSiteInstalled(org, site)} />
}

function Content({ kind, onAction, noAction = false, base, installed = false }: {
  kind: EmptyKind; onAction?: () => void; noAction?: boolean; base?: string; installed?: boolean
}) {
  const copy = uiOnboarding.empty[kind]
  // Once the site receives data, "Install the tracker" is wrong: say why the page is still
  // empty instead and leave only the docs link.
  if (installed && 'installed' in copy) {
    return <EmptyState icon={icons[kind]} title={copy.title} description={copy.installed} docsHref={docsHref} />
  }
  const href = base ? `${base}/settings/${settingsPage[kind]}` : undefined
  const action = noAction ? undefined : onAction ? { label: copy.action, onClick: onAction } : href ? { label: copy.action, href } : undefined
  return <EmptyState icon={icons[kind]} title={copy.title} description={copy.purpose} action={action} docsHref={docsHref} />
}
