import { useState, type FormEvent, type ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { activeLocale } from '@/lib/i18n/intl'

export function AdminPage({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-4" aria-labelledby="admin-page-title">
      <div className="flex flex-col gap-1">
        <h1 id="admin-page-title" className="text-2xl font-semibold">{title}</h1>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {children}
    </section>
  )
}

export function SearchForm({ onSearch }: { onSearch: (value: string) => void }) {
  const [value, setValue] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    onSearch(value)
  }
  return (
    <form role="search" className="flex flex-wrap items-end gap-2" onSubmit={submit}>
      <label className="flex min-w-0 flex-1 flex-col gap-1 text-sm font-medium sm:max-w-sm">
        {en.admin.searchLabel}
        <Input type="search" maxLength={200} value={value} placeholder={en.admin.searchPlaceholder} onChange={(event) => setValue(event.target.value)} />
      </label>
      <Button type="submit" variant="outline" className="min-h-11">{en.admin.searchLabel}</Button>
    </form>
  )
}

export function ErrorLine({ error }: { error: unknown }) {
  if (!error) return null
  return <p role="alert" className="text-sm text-destructive">{error instanceof Error ? error.message : String(error)}</p>
}

export function formatDate(value?: string | null) {
  if (!value) return en.admin.neverSignedIn
  return new Date(value).toLocaleString(activeLocale(), { dateStyle: 'medium', timeStyle: 'short' })
}

export const tableClass = 'w-full min-w-[40rem] text-left text-sm'
export const cellClass = 'px-3 py-2 align-top'
