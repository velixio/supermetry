import type { TrackingPolicy } from '@/features/sites/types'
import type { Role } from '@/components/Sidebar'
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
    <section className="flex flex-col gap-3">
      <h2 className="text-base font-medium">Profiles</h2>
      {policy ? <div className="text-sm text-muted-foreground space-y-1">
        <p>Instance tracking: {policy.instance_mode}. Effective tracking: {policy.effective_mode}.</p>
        <p>Project profile opt-in: {policy.configured_profiles_enabled ? 'on' : 'off'}.</p>
        {policy.instance_mode === 'anonymous' ? <>
          <p>Profiles cannot be enabled while the instance uses anonymous tracking. An instance administrator must select identified tracking and restart the server first.</p>
          <p>Historical identity access is disabled. Safe aggregate analytics remain available; detailed records, recording playback, and potentially identifying dimensions are restricted. Existing data is retained for separately authorized erasure.</p>
        </> : null}
        {policy.restrictions.includes('project_profiles_disabled') ? <p>Profiles are off because this project has not opted in.</p> : null}
      </div> : <FieldDescription>Tracking policy is unavailable. Reload settings before changing profiles.</FieldDescription>}
      <Field data-disabled={!canChange} orientation="horizontal">
        <FieldContent>
          <FieldLabel htmlFor="profiles-enabled">Enable profiles</FieldLabel>
          <FieldDescription>
            This setting controls this project's effective collection mode, not only the Profiles interface. With identified instance tracking and profiles off, collection remains anonymous; new paths outside the declared public path literals are stored as /[redacted]. Enabling profiles on an identified instance permits identifiable data and original paths from new events to be stored. Previously redacted paths cannot be restored.
          </FieldDescription>
          <FieldDescription>{profilesConsentWarning}</FieldDescription>
          {!canManage ? <FieldDescription>Only owners and admins can change this setting.</FieldDescription> : null}
        </FieldContent>
        <Switch
          aria-label="Enable profiles"
          checked={enabled}
          disabled={!canChange}
          id="profiles-enabled"
          onCheckedChange={onChange}
        />
      </Field>
    </section>
  )
}
