import { describe, expect, it } from 'vitest'

import { versionLabel } from './version'

describe('versionLabel', () => {
  it('shows a release version as reported by the server', () => {
    expect(versionLabel('v0.1.10', 'v0.1.9')).toBe('v0.1.10')
  })

  it('adds the v prefix to a bare release version', () => {
    expect(versionLabel('0.1.10', '')).toBe('v0.1.10')
  })

  it('shows the latest release tag and dev for development builds', () => {
    expect(versionLabel('vdev', 'v0.1.10')).toBe('v0.1.10 · dev')
    expect(versionLabel('dev', 'v0.1.10')).toBe('v0.1.10 · dev')
    expect(versionLabel(undefined, 'v0.1.10')).toBe('v0.1.10 · dev')
  })

  it('shows dev alone when no release tag is known', () => {
    expect(versionLabel('vdev', '')).toBe('dev')
  })
})
