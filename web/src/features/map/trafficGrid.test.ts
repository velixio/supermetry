import { describe, expect, it } from 'vitest'

import { dotScale, peakTrafficCell, trafficCells, weekdays } from './trafficGrid'
import { rampStep } from './rampStep'

describe('rampStep', () => {
  it.each([
    [0, 5, 0],
    [1, 5, 1],
    [2, 5, 2],
    [3, 5, 3],
    [4, 5, 4],
    [5, 5, 5],
    [1, 1, 5],
    [1, 0, 0],
  ])('maps %s of %s to step %s', (value, max, expected) => {
    expect(rampStep(value, max)).toBe(expected)
  })
})

describe('trafficCells', () => {
  it('zero-fills every Sunday-first weekday and hour in hour-major order', () => {
    const cells = trafficCells([], 'visitors')

    expect(cells).toHaveLength(168)
    expect(cells.slice(0, 8)).toEqual([
      { weekday: 0, hour: 0, value: 0 },
      { weekday: 1, hour: 0, value: 0 },
      { weekday: 2, hour: 0, value: 0 },
      { weekday: 3, hour: 0, value: 0 },
      { weekday: 4, hour: 0, value: 0 },
      { weekday: 5, hour: 0, value: 0 },
      { weekday: 6, hour: 0, value: 0 },
      { weekday: 0, hour: 1, value: 0 },
    ])
    expect(weekdays).toEqual(['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'])
  })

  it('places a single non-zero API row in its fixed cell', () => {
    const cells = trafficCells([{ weekday: 2, hour_of_day: 15, visitors: 124 }], 'visitors')

    expect(cells[15 * 7 + 2]).toEqual({ weekday: 2, hour: 15, value: 124 })
    expect(cells.filter((cell) => cell.value > 0)).toHaveLength(1)
  })
})

describe('peakTrafficCell', () => {
  it('returns undefined for all-zero traffic', () => {
    expect(peakTrafficCell(trafficCells([], 'visitors'))).toBeUndefined()
  })

  it('keeps the first fixed-order cell when peak values tie', () => {
    const cells = trafficCells(
      [
        { weekday: 5, hour_of_day: 19, visitors: 164 },
        { weekday: 6, hour_of_day: 19, visitors: 164 },
      ],
      'visitors',
    )

    expect(peakTrafficCell(cells)).toEqual({
      weekday: 5,
      hour: 19,
      value: 164,
    })
  })
})

describe('dotScale', () => {
  it('uses the faint six-pixel base dot for zero', () => {
    expect(dotScale(0, 1)).toEqual({ diameter: 6, opacity: 0.2 })
  })

  it('makes non-zero dot area proportional up to an eighteen-pixel maximum', () => {
    expect(dotScale(1, 1)).toEqual({ diameter: 18, opacity: 1 })
    expect(dotScale(0.25, 1)).toEqual({ diameter: 9, opacity: 0.4 })
  })
})
