import { Link, useNavigate } from '@tanstack/react-router'

import { cn } from '@/lib/utils'
import { uiSettings } from '@/messages/uiSettings'

type Target =
  | { kind: 'org'; value: string; label: string; to: string }
  | { kind: 'link'; label: string; to: string; params?: Record<string, string> }

type Group = { id: string; label: string; items: Target[] }

/** `value` is the last path segment of the org settings route; `settings` is the overview. */
export function settingsGroups({ showReplay, canManage, importsSite }: { showReplay: boolean; canManage: boolean; importsSite?: string }, org: string): Group[] {
  const t = uiSettings.nav
  const base = `/app/${org}/settings`
  const item = (value: string, label: string, path = value): Target => ({ kind: 'org', value, label, to: `${base}/${path}` })
  if (!canManage) {
    return [{ id: 'general', label: t.groups.general, items: [{ kind: 'org', value: 'settings', label: t.overview, to: base }] }]
  }
  return [
    { id: 'general', label: t.groups.general, items: [{ kind: 'org', value: 'settings', label: t.overview, to: base }, item('notifications', t.notifications)] },
    { id: 'people', label: t.groups.people, items: [item('members', t.members), item('invites', t.invitations)] },
    { id: 'developers', label: t.groups.developers, items: [item('api-keys', t.apiKeys), item('mcp', t.mcp), item('api', t.apiReference)] },
    { id: 'security', label: t.groups.security, items: [item('audit', t.audit), { kind: 'link', label: t.sessions, to: '/app/account/sessions' }] },
    {
      id: 'data',
      label: t.groups.data,
      items: [
        ...(showReplay ? [item('replay-storage', t.replayStorage)] : []),
        item('exports', t.exports),
        ...(importsSite ? [{ kind: 'link' as const, label: t.imports, to: `/app/${org}/${importsSite}/settings/imports` }] : []),
      ],
    },
  ]
}

const linkClass = 'flex min-h-9 items-center rounded-md px-2.5 text-sm text-muted-foreground outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-[current=page]:bg-muted aria-[current=page]:font-medium aria-[current=page]:text-foreground motion-reduce:transition-none'

export function SettingsNav({ org, current, showReplay = false, canManage = true, importsSite }: {
  org: string
  current: string
  showReplay?: boolean
  canManage?: boolean
  /** Slug of the site whose imports page the "Imports" link opens. */
  importsSite?: string
}) {
  const t = uiSettings.nav
  const navigate = useNavigate()
  const groups = settingsGroups({ showReplay, canManage, importsSite }, org)
  const orgItems = groups.flatMap((group) => group.items).filter((entry): entry is Extract<Target, { kind: 'org' }> => entry.kind === 'org')
  const selected = orgItems.find((entry) => entry.value === current)?.to ?? ''
  return (
    <>
      <div className="lg:hidden">
        <label htmlFor="settings-section" className="sr-only">{t.mobileLabel}</label>
        <select
          id="settings-section"
          className="max-sm:min-h-11 w-full rounded-md border border-input bg-background px-3 text-sm"
          value={selected}
          onChange={(event) => void navigate({ to: event.target.value })}
        >
          {groups.map((group) => (
            <optgroup key={group.id} label={group.label}>
              {group.items.map((entry) => (
                <option key={entry.to} value={entry.to}>{entry.label}</option>
              ))}
            </optgroup>
          ))}
        </select>
      </div>
      <nav aria-label={t.label} className="hidden lg:block">
        <div className="flex flex-col gap-5">
          {groups.map((group) => (
            <div key={group.id} role="group" aria-labelledby={`settings-nav-${group.id}`} className="flex flex-col gap-1">
              <p id={`settings-nav-${group.id}`} className="px-2.5 text-xs font-medium tracking-wide text-muted-foreground uppercase">{group.label}</p>
              {group.items.map((entry) => (
                <Link
                  key={entry.to}
                  to={entry.to}
                  className={cn(linkClass)}
                  aria-current={entry.kind === 'org' && entry.value === current ? 'page' : undefined}
                >
                  {entry.label}
                </Link>
              ))}
            </div>
          ))}
        </div>
      </nav>
    </>
  )
}
