import { describe, expect, it } from 'vitest'

import { heatmapSearch } from '@/routes/app.$org.$site.heatmaps'

describe('heatmap route search', () => {
  it('keeps the period, path, and device in validated URL state', () => {
    expect(heatmapSearch.parse({ period: '7d', path: '/pricing', device: 'mobile' })).toMatchObject({
      period: '7d',
      path: '/pricing',
      device: 'mobile',
    })
  })

  it('uses safe defaults for invalid heatmap URL state', () => {
    expect(heatmapSearch.parse({ device: 'television' })).toMatchObject({
      period: '30d',
      path: '',
      device: 'desktop',
    })
  })
})
