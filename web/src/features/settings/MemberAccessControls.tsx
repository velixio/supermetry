import { useId } from 'react'
import { MemberRoleSelect, type MemberRole } from './MemberRoleSelect'

export const projectGrants = [
  {
    value: 'profiles.read',
    label: 'Read profiles',
    description: 'View identified visitors, traits, and profile history.',
  },
  {
    value: 'replay.read',
    label: 'Watch replays',
    description: 'View recorded sessions and replay links.',
  },
  {
    value: 'data.export',
    label: 'Export data',
    description:
      'Download CSV and site exports. Sensitive exports also require the matching read permission.',
  },
] as const

export type MemberAccess = {
  role: MemberRole
  site_ids: number[] | null
  capabilities: string[]
}

export function siteAccessLabel(siteIds?: number[] | null): string {
  return siteIds == null
    ? 'All current and future sites'
    : siteIds.length === 0
      ? 'No sites'
      : `${siteIds.length} selected site${siteIds.length === 1 ? '' : 's'}`
}

export function MemberAccessControls({
  value,
  actorRole,
  sites,
  disabled = false,
  allowOwner = true,
  onChange,
}: {
  value: MemberAccess
  actorRole: MemberRole
  sites: { id: number; name: string }[]
  disabled?: boolean
  allowOwner?: boolean
  onChange: (changes: Partial<MemberAccess>) => void
}) {
  const id = useId()
  const preset = value.role === 'owner' || value.role === 'admin'
  const scope =
    value.site_ids === null
      ? 'all'
      : value.site_ids.length === 0
        ? 'none'
        : 'selected'
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        <label className="flex flex-col gap-1 text-sm">
          Role
          <MemberRoleSelect
            allowOwner={allowOwner}
            actorRole={actorRole}
            value={value.role}
            disabled={disabled}
            onChange={(role) => onChange({ role })}
          />
        </label>
        <label className="flex flex-col gap-1 text-sm" htmlFor={`${id}-sites`}>
          Site access
          <select
            id={`${id}-sites`}
            className="h-9 rounded-lg border bg-background px-3 text-sm disabled:opacity-50"
            value={preset ? 'all' : scope}
            disabled={disabled || preset}
            onChange={(event) =>
              onChange({
                site_ids:
                  event.target.value === 'all'
                    ? null
                    : event.target.value === 'none'
                      ? []
                      : sites.map((site) => site.id),
              })
            }
          >
            <option value="all">All current and future sites</option>
            <option value="selected" disabled={sites.length === 0}>
              Selected sites
            </option>
            <option value="none">No sites</option>
          </select>
        </label>
      </div>
      {!preset && scope === 'selected' ? (
        <fieldset className="flex flex-wrap gap-3">
          <legend className="mb-2 text-xs text-muted-foreground">
            Selected sites
          </legend>
          {sites.map((site) => (
            <label className="flex items-center gap-2 text-sm" key={site.id}>
              <input
                type="checkbox"
                disabled={disabled}
                checked={value.site_ids?.includes(site.id) === true}
                onChange={(event) =>
                  onChange({
                    site_ids: event.target.checked
                      ? [...(value.site_ids ?? []), site.id]
                      : (value.site_ids ?? []).filter((id) => id !== site.id),
                  })
                }
              />
              {site.name}
            </label>
          ))}
        </fieldset>
      ) : null}
      {!preset && scope === 'none' ? (
        <p className="text-sm text-muted-foreground">
          No project data access. Organization and account pages remain
          available.
        </p>
      ) : null}
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium">
          Sensitive data permissions
        </legend>
        {projectGrants.map((grant) => (
          <label key={grant.value} className="flex items-start gap-2 text-sm">
            <input
              className="mt-1"
              type="checkbox"
              checked={preset || value.capabilities.includes(grant.value)}
              disabled={disabled || preset}
              onChange={(event) =>
                onChange({
                  capabilities: event.target.checked
                    ? [...value.capabilities, grant.value]
                    : value.capabilities.filter((item) => item !== grant.value),
                })
              }
            />
            <span>
              {grant.label}
              <span className="block text-xs text-muted-foreground">
                {grant.description}
              </span>
            </span>
          </label>
        ))}
        {preset ? (
          <p className="text-xs text-muted-foreground">
            Included in the {value.role === 'owner' ? 'Owner' : 'Admin'} role.
            Explicit grants are preserved if the role changes.
          </p>
        ) : null}
      </fieldset>
    </div>
  )
}
