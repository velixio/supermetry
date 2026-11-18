import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { RuleBuilder } from './RuleBuilder'
import { errorAt, parseFieldError } from './ruleTypes'

const channels = [{ id: 8, name: 'Operations', kind: 'email', enabled: true }, { id: 9, name: 'Hooks', kind: 'webhook', enabled: true }]
const assignees = [{ user_id: 2, name: 'Ada' }]

function open(props: Partial<Parameters<typeof RuleBuilder>[0]> = {}) {
  const onSave = vi.fn().mockResolvedValue(undefined)
  render(<RuleBuilder alert={null} channels={channels} assignees={assignees} monitorSlugs={['nightly']} error={null} pending={false} onClose={() => undefined} onSave={onSave} {...props} />)
  return onSave
}
const pick = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })

describe('RuleBuilder', () => {
  afterEach(cleanup)

  it('shows only the fields of the chosen trigger', () => {
    open()
    expect(screen.getByLabelText('new')).toBeInTheDocument()
    expect(screen.queryByLabelText('Metric')).not.toBeInTheDocument()
    pick('When', 'metric')
    expect(screen.getByLabelText('Metric')).toBeInTheDocument()
    expect(screen.getByLabelText('Critical threshold')).toBeInTheDocument()
    expect(screen.queryByLabelText('new')).not.toBeInTheDocument()
    pick('When', 'monitor')
    expect(screen.getByLabelText('nightly')).toBeInTheDocument()
    expect(screen.queryByLabelText('Critical threshold')).not.toBeInTheDocument()
    pick('When', 'event')
    expect(screen.getByLabelText('More than')).toBeInTheDocument()
    expect(screen.queryByLabelText('nightly')).not.toBeInTheDocument()
  })

  it('hides assign and notify-owner for a metric trigger, and the tag and owner filters', () => {
    open()
    expect(within(screen.getByLabelText('Action 1 type')).getByRole('option', { name: 'Assign the issue to a member' })).toBeInTheDocument()
    expect(screen.getByLabelText('Owner')).toBeInTheDocument()
    pick('When', 'metric')
    const options = within(screen.getByLabelText('Action 1 type')).getAllByRole('option').map((o) => o.textContent)
    expect(options).toEqual(['Send to channels', 'Call a webhook'])
    expect(screen.queryByLabelText('Owner')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add tag filter' })).not.toBeInTheDocument()
  })

  it('saves a metric rule with the chosen channel', () => {
    const onSave = open()
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Crash spike' } })
    pick('When', 'metric')
    pick('Metric', 'crash_free_users')
    pick('Alert when it goes', 'below')
    fireEvent.change(screen.getByLabelText('Critical threshold'), { target: { value: '95' } })
    fireEvent.click(screen.getByLabelText('Operations'))
    fireEvent.click(screen.getByRole('button', { name: 'Save rule' }))
    expect(onSave).toHaveBeenCalledTimes(1)
    const input = onSave.mock.calls[0][0]
    expect(input).toMatchObject({ kind: 'app_rule', name: 'Crash spike', enabled: true, channel_ids: [] })
    expect(input.rule.trigger.metric).toMatchObject({ name: 'crash_free_users', op: 'below', critical: 95, window_minutes: 60 })
    expect(input.rule.actions).toEqual([{ type: 'channel', channel_ids: [8] }])
    expect(input.rule.notify_on_resolve).toBe(true)
    expect(input.rule.filters).toEqual({})
  })

  it('renders an API validation error next to its field', () => {
    open({ error: new Error('invalid alert configuration: trigger.metric.window_minutes must be whole hours between 60 and 1440') })
    pick('When', 'metric')
    const field = screen.getByLabelText('Window (minutes)').closest('[data-slot="field"]') as HTMLElement
    expect(within(field).getByText('must be whole hours between 60 and 1440')).toBeInTheDocument()
  })

  it('shows an error that names no field once, at the bottom', () => {
    open({ error: new Error('request failed') })
    expect(screen.getByText('request failed')).toBeInTheDocument()
  })
})

describe('field error paths', () => {
  it('parses the path and matches parents and children', () => {
    const message = 'invalid alert configuration: actions[1].channel_ids needs at least one channel'
    expect(parseFieldError(message)).toEqual({ path: 'actions[1].channel_ids', text: 'needs at least one channel' })
    expect(errorAt(message, 'actions[1]')).toBe('needs at least one channel')
    expect(errorAt(message, 'actions[0]')).toBeUndefined()
    expect(errorAt(message, 'trigger')).toBeUndefined()
    expect(parseFieldError('boom')).toBeNull()
  })
})
