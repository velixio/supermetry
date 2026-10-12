import { describe, expect, it } from 'vitest'

import { fitReplayViewport } from './replay-fit'

describe('fitReplayViewport', () => {
  it('fits a wide desktop recording by width and centres it vertically', () => {
    expect(fitReplayViewport({ width: 1440, height: 900 }, { width: 800, height: 600 })).toEqual({
      scale: 5 / 9,
      width: 800,
      height: 500,
      left: 0,
      top: 50,
    })
  })

  it('fits a portrait recording by height and centres it horizontally', () => {
    const fit = fitReplayViewport({ width: 390, height: 844 }, { width: 800, height: 600 })

    expect(fit.scale).toBeCloseTo(600 / 844)
    expect(fit.width).toBeCloseTo(390 * (600 / 844))
    expect(fit.height).toBe(600)
    expect(fit.left).toBeCloseTo((800 - fit.width) / 2)
    expect(fit.top).toBe(0)
  })

  it('scales down when the stage is smaller than the recording', () => {
    expect(fitReplayViewport({ width: 1024, height: 768 }, { width: 320, height: 240 })).toEqual({
      scale: 0.3125,
      width: 320,
      height: 240,
      left: 0,
      top: 0,
    })
  })

  it.each([
    [{ width: 0, height: 900 }, { width: 800, height: 600 }],
    [{ width: 1440, height: 0 }, { width: 800, height: 600 }],
    [{ width: 1440, height: 900 }, { width: 0, height: 600 }],
    [{ width: 1440, height: 900 }, { width: 800, height: 0 }],
  ])('returns an empty fit for zero sizes', (viewport, stage) => {
    expect(fitReplayViewport(viewport, stage)).toEqual({ scale: 0, width: 0, height: 0, left: 0, top: 0 })
  })
})
