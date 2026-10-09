import { Link } from '@tanstack/react-router'

const organizationSettingsTabs = [
  ['settings', 'Overview', '/$org/settings'],
  ['members', 'Members', '/$org/settings/members'],
  ['invites', 'Invitations', '/$org/settings/invites'],
  ['api-keys', 'API keys', '/$org/settings/api-keys'],
  ['api', 'API reference', '/$org/settings/api'],
  ['audit', 'Audit log', '/$org/settings/audit'],
  ['notifications', 'Notifications', '/$org/settings/notifications'],
  ['replay-storage', 'Replay storage', '/$org/settings/replay-storage'],
] as const

export function OrganizationSettingsTabs({ org, current, showReplay = false, canManage = true }: { org: string; current: string; showReplay?: boolean; canManage?: boolean }) {
  return (
    <nav aria-label="Organization settings" className="overflow-x-auto border-b">
      <div role="tablist" className="flex min-w-max gap-5">
        {organizationSettingsTabs.filter(([value]) => value === 'settings' || (canManage && (value !== 'replay-storage' || showReplay))).map(([value, label, to]) => (
          <Link
            key={value}
            aria-current={current === value ? 'page' : undefined}
            className="border-b-2 border-transparent px-0.5 py-3 text-sm font-medium text-muted-foreground aria-current-page:border-accent aria-current-page:text-foreground"
            to={to}
            params={{ org }}
          >
            {label}
          </Link>
        ))}
      </div>
    </nav>
  )
}
