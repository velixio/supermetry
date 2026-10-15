import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { estimate, stepIndex, UpdateProgress, updateSteps } from './UpdateProgress'
import type { AdminUpdateJob } from './adminApi'

afterEach(() => cleanup())

function job(status: AdminUpdateJob['status']): AdminUpdateJob {
  return {
    id: 4, from_version: 'v1.2.0', target_version: 'v1.3.0', deployment_mode: 'binary',
    status, created_at: '2026-10-14T09:01:00Z', updated_at: new Date().toISOString(),
  } as AdminUpdateJob
}

describe('update estimate', () => {
  const total = updateSteps.reduce((sum, step) => sum + step.seconds, 0)

  it('maps a job status to its step, with the request before the first step', () => {
    expect(stepIndex('requested')).toBe(0)
    expect(stepIndex('backing_up')).toBe(3)
    expect(stepIndex('stopping')).toBe(4)
    expect(stepIndex('complete')).toBe(updateSteps.length)
  })

  it('counts the remaining estimate from the current step onwards', () => {
    expect(estimate(0, 0).remaining).toBe(total)
    // Backing up for 5 of its 20 seconds, then stopping, activating, restarting and health: 15 + 2 + 1 + 3 + 3.
    expect(estimate(3, 5).remaining).toBe(24)
    expect(estimate(updateSteps.length - 1, 0).remaining).toBe(3)
  })

  it('flags a step that outlasts its estimate and never reports 100% early', () => {
    expect(estimate(3, 30).overdue).toBe(true)
    expect(estimate(3, 30).remaining).toBe(9)
    expect(estimate(3, 5).overdue).toBe(false)
    expect(estimate(updateSteps.length - 1, 999).percent).toBeLessThan(100)
  })
})

describe('UpdateProgress', () => {
  it('lists every step in order, marking finished, current and waiting ones', () => {
    render(<UpdateProgress job={job('backing_up')} reconnecting={false} />)
    const items = within(screen.getByRole('list', { name: 'Update steps' })).getAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual([
      'Checking eligibility and disk spacedone', 'Downloading the releasedone', 'Verifying the signaturedone',
      'Backing up the databasein progress', 'Stopping Supermetrywaiting', 'Activating the releasewaiting',
      'Starting the new versionwaiting', 'Checking healthwaiting',
    ])
    expect(items[3]).toHaveAttribute('aria-current', 'step')
    expect(screen.getByRole('progressbar', { name: 'Update progress' })).toBeInTheDocument()
  })

  it('warns that Supermetry will go offline before it does, then says it is offline', () => {
    const { rerender } = render(<UpdateProgress job={job('downloading')} reconnecting={false} />)
    expect(screen.getByText('Supermetry will be offline for a few seconds')).toBeVisible()
    rerender(<UpdateProgress job={job('stopping')} reconnecting={false} />)
    expect(screen.getByText('Supermetry is offline right now')).toBeVisible()
    rerender(<UpdateProgress job={job('restarting')} reconnecting />)
    expect(screen.getByText('Reconnecting…')).toBeVisible()
  })
})
