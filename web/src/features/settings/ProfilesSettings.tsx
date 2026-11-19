import type { TrackingPolicy } from '@/features/sites/types'
import type { Role } from '@/components/Sidebar'
import { InfoTip } from '@/components/InfoTip'
import { Field, FieldContent, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'

export const profilesConsentWarning = 'Identifies individual users. Only enable this if your users have consented to analytics that can identify them.'

export function ProfilesSettings({
  enabled,
  onChange,
  role,
  policy,
}: {
  enabled: boolean
  onChange: (enabled: boolean) => void
  role: Role
  policy?: TrackingPolicy
}) {
  const canManage = role === 'owner' || role === 'admin'
  const canChange = canManage && policy?.instance_mode === 'identified'
  return (
    <Field data-disabled={!canChange} orientation="horizontal" className="rounded-md bg-muted/50 p-3">
      <FieldContent>
        <div className="flex items-center gap-1.5">
          <FieldLabel htmlFor="profiles-enabled">Enable profiles</FieldLabel>
          <InfoTip label="About profiles">
            <span className="flex flex-col gap-1.5">
              <span>This controls this project&apos;s effective collection mode, not only the Profiles view. With identified instance tracking and profiles off, collection stays anonymous: new paths outside the declared public paths are stored as /[redacted].</span>
              <span>Enabling profiles permits identifiable data and original paths from new events to be stored. Previously redacted paths cannot be restored.</span>
              {policy?.instance_mode === 'anonymous' ? <span>Historical identity access is disabled. Safe aggregate analytics remain available; detailed records, recording playback and identifying dimensions are restricted. Existing data is kept for separately authorized erasure.</span> : null}
            </span>
          </InfoTip>
        </div>
        <FieldDescription>{profilesConsentWarning}</FieldDescription>
        {policy ? (
          <FieldDescription>
            Tracking: {policy.effective_mode} (instance {policy.instance_mode}, project opt-in {policy.configured_profiles_enabled ? 'on' : 'off'}).
            {policy.instance_mode === 'anonymous' ? ' An instance admin must select identified tracking first.' : ''}
          </FieldDescription>
        ) : <FieldDescription>Tracking policy unavailable. Reload before changing profiles.</FieldDescription>}
        {!canManage ? <FieldDescription>Only owners and admins can change this.</FieldDescription> : null}
      </FieldContent>
      <Switch
        aria-label="Enable profiles"
        checked={enabled}
        disabled={!canChange}
        id="profiles-enabled"
        onCheckedChange={onChange}
      />
    </Field>
  )
}
