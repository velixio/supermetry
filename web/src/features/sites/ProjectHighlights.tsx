import { Fragment } from 'react'

import { cn } from '@/lib/utils'
import { fmt } from '@/lib/format'
import { formatNumber, interpolate } from '@/lib/i18n'
import { en } from '@/messages/en'

import type { OverviewSite, ProjectHighlight } from './api'

/** The sentence for one typed fact, worded through the message catalogue. */
export function highlightText(highlight: ProjectHighlight): string {
  const t = en.sitesOverview.highlights
  switch (highlight.kind) {
    case 'quiet': return interpolate(t.quiet, { hours: highlight.hours })
    case 'new_issues': return interpolate(t.newIssues, { count: highlight.count })
    case 'crash_free_drop': return interpolate(t.crashFreeDrop, { points: formatNumber(highlight.points, { maximumFractionDigits: 1 }) })
    case 'visitors_change': {
      const template = highlight.percent < 0 ? t.visitorsDown : t.visitorsUp
      return interpolate(template, { percent: fmt.percent(Math.abs(highlight.percent)) })
    }
    case 'top_source': return interpolate(t.topSource, { name: highlight.name })
    case 'steady': return t.steady
  }
}

/** All facts of a project joined with " · ", for the card's accessible name. */
export function highlightsSummary(site: OverviewSite): string {
  return (site.highlights ?? []).map(highlightText).join(' · ')
}

/** The muted status line under a project name; the quiet fact takes the warning tone. */
export function ProjectHighlights({ site, className }: { site: OverviewSite; className?: string }) {
  const items = site.highlights ?? []
  if (items.length === 0) return null
  return (
    <p className={cn('text-sm text-muted-foreground', className)} data-testid="project-highlights">
      {items.map((item, index) => (
        <Fragment key={item.kind}>
          {index > 0 ? ' · ' : null}
          <span className={item.kind === 'quiet' ? 'text-warning' : undefined}>{highlightText(item)}</span>
        </Fragment>
      ))}
    </p>
  )
}
