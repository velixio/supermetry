import { describe, expect, it } from 'vitest'

import { en } from '@/messages/en'
import { dashEdit } from '@/messages/dashEdit'
import { fullReport } from '@/messages/fullReport'
import { obsLogs } from '@/messages/obsLogs'
import { obsMetrics } from '@/messages/obsMetrics'
import { ui } from '@/messages/ui'
import { uiAccount } from '@/messages/uiAccount'
import { uiAlerts } from '@/messages/uiAlerts'
import { contextPanel } from '@/messages/contextPanel'
import { uiFlows } from '@/messages/uiFlows'
import { uiFoundation } from '@/messages/uiFoundation'

import { isTitleCase } from './sentenceCase'

function walk(node: unknown, path: string, out: [string, string][]) {
  if (typeof node === 'string') out.push([path, node])
  else if (node && typeof node === 'object') for (const [k, v] of Object.entries(node)) walk(v, `${path}.${k}`, out)
}

describe('English UI strings use sentence case', () => {
  it('detects Title Case', () => {
    expect(isTitleCase('Views Per Visit')).toBe(true)
    expect(isTitleCase('Bounce Rate')).toBe(true)
    expect(isTitleCase('Bounce rate')).toBe(false)
    expect(isTitleCase('Open the Stripe Dashboard to add an endpoint.')).toBe(false) // prose is skipped
    expect(isTitleCase('Views per visit')).toBe(false)
    expect(isTitleCase('Connect GitHub and Google Search Console')).toBe(false)
  })

  it('has no Title Case label in the message catalogues', () => {
    const all: [string, string][] = []
    const modules = { en, dashEdit, fullReport, obsLogs, obsMetrics, ui, uiAccount, uiAlerts, uiFlows, uiFoundation, contextPanel }
    for (const [name, tree] of Object.entries(modules)) walk(tree, name, all)
    const offenders = all.filter(([, text]) => isTitleCase(text)).map(([key, text]) => `${key}: ${text}`)
    expect(offenders).toEqual([])
  })
})
