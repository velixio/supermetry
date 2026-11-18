import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { DeployList, deployMarkers } from './DeployMarkers'
import { dayInZone, formatDeltaPp } from './format'

const deploy = (id: number, at: string, release = '1.1.0') => ({ id, release, environment: 'production', name: '', url: '', at })

describe('deploy markers', () => {
  afterEach(cleanup)

  it('keeps one marker per deploy inside the plotted days and drops the rest', () => {
    const days = ['2026-11-14', '2026-11-15', '2026-11-16']
    const markers = deployMarkers([deploy(1, '2026-11-15T10:00:00Z'), deploy(2, '2026-11-01T10:00:00Z', '1.0.0'), deploy(3, '2026-11-16T09:00:00Z', '1.2.0')], 'UTC', days)
    expect(markers.map((m) => [m.key, m.day, m.label])).toEqual([[1, '2026-11-15', '1.1.0'], [3, '2026-11-16', '1.2.0']])
  })

  it('places a deploy on the local day of the site time zone', () => {
    expect(dayInZone('2026-11-15T20:00:00Z', 'Asia/Kolkata')).toBe('2026-11-16')
    const markers = deployMarkers([deploy(1, '2026-11-15T20:00:00Z')], 'Asia/Kolkata', ['2026-11-15'])
    expect(markers).toEqual([])
  })

  it('lists deploys as text and renders nothing for none', () => {
    const { container, rerender } = render(<DeployList deploys={[deploy(1, '2026-11-15T10:00:00Z')]} timezone="UTC" />)
    expect(screen.getByRole('list', { name: 'Deploys' })).toHaveTextContent('1.1.0 2026-11-15 · production')
    rerender(<DeployList deploys={[]} timezone="UTC" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('formats signed percentage-point deltas', () => {
    expect(formatDeltaPp(-0.03)).toBe('−3.0 pp')
    expect(formatDeltaPp(0.0125)).toBe('+1.3 pp')
    expect(formatDeltaPp(0)).toBe('0.0 pp')
    expect(formatDeltaPp(null)).toBe('n/a')
  })
})
