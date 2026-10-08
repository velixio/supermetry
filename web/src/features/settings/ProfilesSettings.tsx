import type { Role } from '@/components/Sidebar'
import { Field, FieldContent, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'

export const profilesConsentWarning = 'Identifies individual users. Only enable this if your users have consented to analytics that can identify them.'

export function ProfilesSettings({
  enabled,
  onChange,
  role,
}: {
  enabled: boolean
  onChange: (enabled: boolean) => void
  role: Role
}) {
  const canChange = role === 'owner' || role === 'admin'
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-base font-medium">Profiles</h2>
      <Field data-disabled={!canChange} orientation="horizontal">
        <FieldContent>
          <FieldLabel htmlFor="profiles-enabled">Enable profiles</FieldLabel>
          <FieldDescription>{profilesConsentWarning}</FieldDescription>
          {!canChange ? <FieldDescription>Only owners and admins can change this setting.</FieldDescription> : null}
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
