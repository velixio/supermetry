import { describe, expect, it } from 'vitest'

import {
  marginTotals,
  orderedWeekdays,
  peakTrafficWindows,
  peakTrafficCell,
  quietestWeekday,
  trafficCells,
  weekdayShare,
} from './trafficGrid'
import { rampStep } from './rampStep'

describe('rampStep', () => {
  it.each([
    [0, 5, 0], [1, 5, 1], [2, 5, 2], [3, 5, 3], [4, 5, 4], [5, 5, 5], [1, 1, 5], [1, 0, 0],
  ])('maps %s of %s to step %s', (value, max, expected) => {
    expect(rampStep(value, max)).toBe(expected)
  })
})

describe('weekly rhythm ordering', () => {
  it('zero-fills all 168 cells in Monday-first row-major order', () => {
    const cells = trafficCells([], 'visitors', 'en-GB')

    expect(cells).toHaveLength(168)
    expect(cells.slice(0, 25)).toEqual([
      ...Array.from({ length: 24 }, (_, hour) => ({ weekday: 1, hour, value: 0 })),
      { weekday: 2, hour: 0, value: 0 },
    ])
    expect(orderedWeekdays('en-GB').map((day) => day.name)).toEqual([
      'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday',
    ])
  })

  it('uses Sunday first for a Sunday-first locale and places API values in fixed cells', () => {
    const cells = trafficCells([{ weekday: 2, hour_of_day: 15, visitors: 124 }], 'visitors', 'en-US')

    expect(orderedWeekdays('en-US').map((day) => day.name)).toEqual([
      'Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday',
    ])
    expect(cells[2 * 24 + 15]).toEqual({ weekday: 2, hour: 15, value: 124 })
    expect(cells.filter((cell) => cell.value > 0)).toHaveLength(1)
  })
})

describe('weekly rhythm summaries', () => {
  const cells = trafficCells([
    { weekday: 1, hour_of_day: 13, visitors: 89 },
    { weekday: 1, hour_of_day: 14, visitors: 11 },
    { weekday: 2, hour_of_day: 13, visitors: 40 },
    { weekday: 3, hour_of_day: 8, visitors: 5 },
  ], 'visitors', 'en-GB')

  it('computes hand-checked weekday and hour margins', () => {
    expect(marginTotals(cells)).toEqual({
      weekday: [100, 40, 5, 0, 0, 0, 0],
      hour: [0, 0, 0, 0, 0, 0, 0, 0, 5, 0, 0, 0, 0, 129, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0],
    })
  })

  it('finds the peak and keeps the first fixed-order cell when values tie', () => {
    const tied = trafficCells([
      { weekday: 5, hour_of_day: 19, visitors: 164 },
      { weekday: 6, hour_of_day: 19, visitors: 164 },
    ], 'visitors', 'en-GB')

    expect(peakTrafficCell(tied)).toEqual({ weekday: 5, hour: 19, value: 164 })
  })

  it('finds the first quietest weekday on a tie and reports no summary for all-zero weeks', () => {
    expect(quietestWeekday(cells)).toEqual({ weekday: 4, value: 0 })
    const zero = trafficCells([], 'visitors', 'en-GB')
    expect(peakTrafficCell(zero)).toBeUndefined()
    expect(quietestWeekday(zero)).toBeUndefined()
  })

  it('returns the top three non-zero windows in fixed-order tie order', () => {
    const tied = trafficCells([
      { weekday: 1, hour_of_day: 9, visitors: 30 },
      { weekday: 1, hour_of_day: 10, visitors: 30 },
      { weekday: 2, hour_of_day: 8, visitors: 20 },
      { weekday: 3, hour_of_day: 7, visitors: 10 },
    ], 'visitors', 'en-GB')

    expect(peakTrafficWindows(tied)).toEqual([
      { weekday: 1, hour: 9, value: 30 },
      { weekday: 1, hour: 10, value: 30 },
      { weekday: 2, hour: 8, value: 20 },
    ])
  })

  it('returns fewer than three peaks when needed and none for an all-zero week', () => {
    const sparse = trafficCells([
      { weekday: 6, hour_of_day: 12, visitors: 7 },
      { weekday: 0, hour_of_day: 12, visitors: 4 },
    ], 'visitors', 'en-GB')

    expect(peakTrafficWindows(sparse)).toEqual([
      { weekday: 6, hour: 12, value: 7 },
      { weekday: 0, hour: 12, value: 4 },
    ])
    expect(peakTrafficWindows(trafficCells([], 'visitors', 'en-GB'))).toEqual([])
  })

  it('computes the hand-checked weekday share and handles an all-zero week', () => {
    const mixed = trafficCells([
      { weekday: 1, hour_of_day: 9, visitors: 40 },
      { weekday: 5, hour_of_day: 17, visitors: 20 },
      { weekday: 6, hour_of_day: 12, visitors: 30 },
      { weekday: 0, hour_of_day: 12, visitors: 10 },
    ], 'visitors', 'en-GB')

    expect(weekdayShare(mixed)).toBe(60)
    expect(weekdayShare(trafficCells([], 'visitors', 'en-GB'))).toBe(0)
  })
})
