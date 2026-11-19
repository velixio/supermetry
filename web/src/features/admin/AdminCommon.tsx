import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useState, type FormEvent, type ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { AdminSort } from './adminApi'
import { en } from '@/messages/en'
import { activeLocale } from '@/lib/i18n/intl'

export function AdminPage({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <PageContainer width="narrow">
      <section className="flex min-w-0 flex-col gap-4" aria-labelledby="admin-page-title">
        <PageHeader titleId="admin-page-title" title={title} description={description} />
        {children}
      </section>
    </PageContainer>
  )
}

/** The search box of the admin lists. `initial` comes from the URL (?q=) so results can be linked and survive Back. */
export function SearchForm({ onSearch, initial = '' }: { onSearch: (value: string) => void; initial?: string }) {
  const [value, setValue] = useState(initial)
  useEffect(() => setValue(initial), [initial])
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
      <Button type="submit" variant="outline" className="max-sm:min-h-11">{en.admin.searchLabel}</Button>
    </form>
  )
}

export function ErrorLine({ error }: { error: unknown }) {
  if (!error) return null
  return <p role="alert" className="text-sm text-destructive">{error instanceof Error ? error.message : String(error)}</p>
}

export function formatDate(value?: string | null, empty: string = en.admin.neverSignedIn) {
  if (!value) return empty
  return new Date(value).toLocaleString(activeLocale(), { dateStyle: 'medium', timeStyle: 'short' })
}

/** Search state for an admin list: the URL's ?q= when the route provides it, local state otherwise. */
export function useAdminSearch(search?: string, onSearch?: (value: string) => void): [string, (value: string) => void] {
  const [local, setLocal] = useState('')
  return search !== undefined && onSearch ? [search, onSearch] : [local, setLocal]
}

export type AdminListSearch = { q?: string; sort?: string; order?: 'asc' | 'desc' }

export function parseAdminSearch(value: Record<string, unknown>): AdminListSearch {
  const out: AdminListSearch = {}
  if (typeof value.q === 'string' && value.q.trim()) out.q = value.q.slice(0, 200)
  if (typeof value.sort === 'string' && /^[a-z_]{1,20}$/.test(value.sort)) out.sort = value.sort
  if (value.order === 'asc' || value.order === 'desc') out.order = value.order
  return out
}

/** Sort state of an admin list: the URL's ?sort=&order= when the route provides it, local state otherwise. */
export function useAdminSort(sort?: AdminSort, onSort?: (sort: AdminSort) => void): [AdminSort, (next: AdminSort) => void] {
  const [local, setLocal] = useState<AdminSort>({})
  return onSort ? [sort ?? {}, onSort] : [local, setLocal]
}

/** A table header that sorts the list on the server; `field` is the API sort key. */
export function SortHeader({ field, label, sort, onSort }: { field: string; label: ReactNode; sort: AdminSort; onSort: (sort: AdminSort) => void }) {
  const active = sort.sort === field
  const order = active ? sort.order ?? 'asc' : undefined
  return (
    <th scope="col" className={cellClass} aria-sort={order === 'asc' ? 'ascending' : order === 'desc' ? 'descending' : undefined}>
      <button type="button" className="inline-flex items-center gap-1 uppercase max-sm:min-h-11" onClick={() => onSort({ sort: field, order: active && order === 'asc' ? 'desc' : 'asc' })}>
        {label}<span aria-hidden="true">{order === 'asc' ? '\u2191' : order === 'desc' ? '\u2193' : ''}</span>
      </button>
    </th>
  )
}

/** "Load more" under a cursor-paged list; renders nothing on the last page. */
export function LoadMore({ query }: { query: { hasNextPage?: boolean; isFetchingNextPage?: boolean; fetchNextPage?: () => unknown } }) {
  if (!query.hasNextPage) return null
  return <Button type="button" variant="outline" className="self-start max-sm:min-h-11" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage?.()}>{query.isFetchingNextPage ? en.admin.loading : en.admin.loadMore}</Button>
}

export const tableClass = 'w-full min-w-[40rem] text-left text-sm'
export const cellClass = 'px-3 py-2 align-top'
