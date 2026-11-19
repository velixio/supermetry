import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import {
  useAdminEmail,
  useRemoveAdminEmail,
  useSaveAdminEmail,
  useTestAdminEmail,
  type AdminEmailSettings,
} from './adminApi'
import { AdminPage, ErrorLine } from './AdminCommon'

const tlsOptions: { value: AdminEmailSettings['tls']; label: string }[] = [
  { value: 'starttls', label: en.admin.emailTlsStarttls },
  { value: 'implicit', label: en.admin.emailTlsImplicit },
  { value: 'none', label: en.admin.emailTlsNone },
]

function EnvironmentView({ settings }: { settings: AdminEmailSettings }) {
  const rows: [string, string][] = [
    [en.admin.emailHost, settings.host],
    [en.admin.emailPort, String(settings.port)],
    [en.admin.emailTls, tlsOptions.find((option) => option.value === settings.tls)?.label ?? settings.tls],
    [en.admin.emailUser, settings.user],
    [en.admin.emailFrom, settings.from],
    [en.admin.emailHelo, settings.helo],
  ]
  return (
    <div className="flex max-w-xl flex-col gap-3 rounded-lg bg-card p-4">
      <p role="note" className="text-sm text-muted-foreground">{en.admin.emailEnvironmentNote}</p>
      <dl className="grid grid-cols-[minmax(0,10rem)_1fr] gap-x-4 gap-y-2 text-sm">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="font-medium">{label}</dt>
            <dd className="min-w-0 break-words">{value || '—'}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function SettingsForm({ settings }: { settings: AdminEmailSettings }) {
  const save = useSaveAdminEmail()
  const remove = useRemoveAdminEmail()
  const [host, setHost] = useState('')
  const [port, setPort] = useState('587')
  const [tls, setTls] = useState<AdminEmailSettings['tls']>('starttls')
  const [user, setUser] = useState('')
  const [password, setPassword] = useState('')
  const [from, setFrom] = useState('')
  const [helo, setHelo] = useState('')
  useEffect(() => {
    setHost(settings.host)
    setPort(String(settings.port || 587))
    setTls(settings.tls)
    setUser(settings.user)
    setFrom(settings.from)
    setHelo(settings.helo)
    setPassword('')
  }, [settings])
  const body = (extra: { clear_password?: boolean } = {}) => ({
    host: host.trim(), port: Number(port) || 0, tls, user: user.trim(), password, from: from.trim(), helo: helo.trim(), ...extra,
  })
  const submit = (event: FormEvent) => {
    event.preventDefault()
    save.mutate(body())
  }
  return (
    <form onSubmit={submit} className="flex max-w-xl flex-col gap-4 rounded-lg bg-card p-4" aria-label={en.admin.email}>
      {settings.source === 'none' ? <p className="text-sm text-muted-foreground">{en.admin.emailNotConfigured}</p> : null}
      <ErrorLine error={save.error ?? remove.error} />
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.emailHost}
        <Input value={host} required maxLength={253} autoComplete="off" onChange={(event) => setHost(event.target.value)} />
      </label>
      <div className="flex flex-wrap gap-4">
        <label className="flex w-28 flex-col gap-1 text-sm font-medium">
          {en.admin.emailPort}
          <Input type="number" min={1} max={65535} value={port} required onChange={(event) => setPort(event.target.value)} />
        </label>
        <label className="flex min-w-0 flex-1 flex-col gap-1 text-sm font-medium">
          {en.admin.emailTls}
          <select className="max-sm:min-h-11 rounded-lg border border-input bg-background px-3" value={tls} onChange={(event) => setTls(event.target.value as AdminEmailSettings['tls'])}>
            {tlsOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
      </div>
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.emailUser}
        <Input value={user} maxLength={255} autoComplete="off" onChange={(event) => setUser(event.target.value)} />
      </label>
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.emailPassword}
        <Input type="password" value={password} maxLength={1024} autoComplete="new-password" onChange={(event) => setPassword(event.target.value)} />
        {settings.password_set ? <span className="text-xs font-normal text-muted-foreground">{en.admin.emailPasswordSet}</span> : null}
      </label>
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.emailFrom}
        <Input value={from} required maxLength={320} placeholder="Supermetry <supermetry@example.com>" onChange={(event) => setFrom(event.target.value)} />
      </label>
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.emailHelo}
        <Input value={helo} maxLength={253} autoComplete="off" onChange={(event) => setHelo(event.target.value)} />
      </label>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" className="max-sm:min-h-11" disabled={save.isPending}>{en.admin.emailSave}</Button>
        {settings.password_set ? (
          <Button type="button" variant="outline" className="max-sm:min-h-11" disabled={save.isPending} onClick={() => save.mutate({ ...body({ clear_password: true }), password: '' })}>
            {en.admin.emailClearPassword}
          </Button>
        ) : null}
        {settings.source === 'database' ? (
          <Button type="button" variant="outline" className="max-sm:min-h-11" disabled={remove.isPending} onClick={() => remove.mutate()}>{en.admin.emailRemove}</Button>
        ) : null}
        {save.isSuccess ? <span role="status" className="text-sm text-muted-foreground">{en.admin.saved}</span> : null}
      </div>
    </form>
  )
}

function TestForm() {
  const test = useTestAdminEmail()
  const [to, setTo] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    test.mutate(to)
  }
  return (
    <form onSubmit={submit} className="flex max-w-xl flex-col gap-3 rounded-lg bg-card p-4" aria-labelledby="admin-email-test-title">
      <h2 id="admin-email-test-title" className="text-lg font-semibold">{en.admin.emailTestTitle}</h2>
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.emailTestTo}
        <Input type="email" value={to} maxLength={320} placeholder={en.admin.emailTestPlaceholder} onChange={(event) => setTo(event.target.value)} />
      </label>
      <div>
        <Button type="submit" className="max-sm:min-h-11" disabled={test.isPending}>{test.isPending ? en.admin.emailTestSending : en.admin.emailTestSend}</Button>
      </div>
      <ErrorLine error={test.error} />
      {test.data?.ok ? (
        <p role="status" className="text-sm">{en.admin.emailTestOk} <span className="text-muted-foreground">{test.data.note}</span></p>
      ) : null}
      {test.data && !test.data.ok ? (
        <p role="alert" className="text-sm text-destructive">{en.admin.emailTestFailed}: {test.data.error}</p>
      ) : null}
    </form>
  )
}

export function AdminEmailPage() {
  const email = useAdminEmail()
  return (
    <AdminPage title={en.admin.email} description={en.admin.emailDescription}>
      <ErrorLine error={email.error} />
      {email.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {email.data ? (email.data.source === 'environment' ? <EnvironmentView settings={email.data} /> : <SettingsForm settings={email.data} />) : null}
      {email.data && (email.data.source !== 'none') ? <TestForm /> : null}
    </AdminPage>
  )
}
