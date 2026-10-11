import { describe, expect, it } from 'vitest'

import { placeTooltip } from './ChartTooltip'

describe('placeTooltip', () => {
  it('places a tooltip above its anchor and moves it above an overlapping axis', () => {
    expect(placeTooltip(
      { left: 100, right: 120, top: 100, bottom: 120 },
      { width: 80, height: 30 },
      { width: 300, height: 200 },
      { left: 0, right: 300, top: 60, bottom: 85 },
    )).toMatchObject({ left: 70, top: 22, placement: 'above' })
  })

  it('keeps the tooltip inside the viewport and flips below when there is no room above', () => {
    expect(placeTooltip(
      { left: 0, right: 10, top: 4, bottom: 20 },
      { width: 80, height: 30 },
      { width: 300, height: 200 },
    )).toMatchObject({ left: 8, top: 28, placement: 'below' })
  })
})
