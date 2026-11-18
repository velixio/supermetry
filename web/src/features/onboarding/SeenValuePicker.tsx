import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { useSuggestions } from '@/features/events/api'
import { uiOnboarding } from '@/messages/uiOnboarding'

export type SuggestSource = { siteId: number; orgId: number; dateRange: { from: string; to: string } }

const pickLimit = 8
const delayMs = 250

/**
 * Offers page paths or event names the site has already received, filtered by what the user has
 * typed so far. Free text stays possible: the picker only fills the field next to it.
 */
export function SeenValuePicker({ source, dimension, query, onPick, id }: {
  source: SuggestSource
  dimension: 'page' | 'event'
  query: string
  onPick: (value: string) => void
  id?: string
}) {
  const [debounced, setDebounced] = useState(query)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), delayMs)
    return () => clearTimeout(timer)
  }, [query])
  const suggestions = useSuggestions(source.siteId, source.orgId, source.dateRange, dimension, debounced.trim())
  if (suggestions.isPending || suggestions.isError) return null
  const items = suggestions.data.slice(0, pickLimit)
  const text = uiOnboarding.picker
  if (items.length === 0) return <p id={id} className="text-xs text-muted-foreground">{text.none}</p>
  const label = dimension === 'page' ? text.pageSuggestion : text.eventSuggestion
  return (
    <div id={id} role="group" aria-label={text.legend} className="flex flex-wrap items-center gap-1.5">
      <span aria-hidden="true" className="text-xs text-muted-foreground">{text.legend}</span>
      {items.map((item) => (
        <Button
          key={item.value}
          type="button"
          size="sm"
          variant="secondary"
          className="max-w-full"
          aria-label={label.replace('{value}', item.value).replace('{count}', String(item.count))}
          onClick={() => onPick(item.value)}
        >
          <span className="truncate">{item.value}</span>
        </Button>
      ))}
    </div>
  )
}
