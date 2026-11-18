import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { IncidentList, type AlertIncident } from './IncidentList'

const base: AlertIncident = { id: 1, alert_id: 4, site_id: 2, group_key: 'metric', severity: 'critical', opened_at: '2026-11-20T10:00:00Z', last_value: 12, peak_value: 31, threshold: 10 }

describe('IncidentList', () => {
  afterEach(cleanup)

  it('shows opened and resolved times, the peak and whether a notice went out', () => {
    render(<IncidentList incidents={[
      { ...base, resolved_at: '2026-11-20T10:40:00Z', notified_at: '2026-11-20T10:01:00Z' },
      { ...base, id: 2, severity: 'warning' },
    ]} />)
    const rows = within(screen.getByRole('list', { name: 'Incidents' })).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Resolved')
    expect(rows[0]).toHaveTextContent('Peak 31')
    expect(rows[0]).toHaveTextContent(/Notified/)
    expect(rows[1]).toHaveTextContent('Open')
    expect(rows[1]).toHaveTextContent('No notice sent')
  })

  it('has an empty state', () => {
    render(<IncidentList incidents={[]} />)
    expect(screen.getByText('No incidents for this rule yet.')).toBeInTheDocument()
  })
})
