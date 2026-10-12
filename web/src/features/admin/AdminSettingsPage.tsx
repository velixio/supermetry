import { en } from '@/messages/en'
import { useAdminSettings, useUpdateAdminSettings } from './adminApi'
import { AdminPage, ErrorLine } from './AdminCommon'

export function AdminSettingsPage() {
  const settings = useAdminSettings()
  const update = useUpdateAdminSettings()
  return (
    <AdminPage title={en.admin.settings}>
      <ErrorLine error={settings.error ?? update.error} />
      {settings.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {settings.data ? (
        <div className="flex max-w-xl flex-col gap-5 rounded-lg border bg-card p-4">
          <label className="flex flex-col gap-1 text-sm font-medium">
            {en.admin.signupMode}
            <select className="h-11 rounded-lg border border-input bg-background px-3" value={settings.data.signup_mode} disabled={update.isPending}
              onChange={(event) => update.mutate({ signup_mode: event.target.value as 'invite' | 'open' })}>
              <option value="invite">{en.admin.signupInvite}</option>
              <option value="open">{en.admin.signupOpen}</option>
            </select>
          </label>
          <label className="flex min-h-11 items-center gap-3 text-sm font-medium">
            <input type="checkbox" className="size-5" checked={settings.data.replay_visible} disabled={update.isPending}
              onChange={(event) => update.mutate({ replay_visible: event.target.checked })} />
            {en.admin.replayVisible}
          </label>
          {update.isSuccess ? <p role="status" className="text-sm text-muted-foreground">{en.admin.saved}</p> : null}
        </div>
      ) : null}
    </AdminPage>
  )
}
