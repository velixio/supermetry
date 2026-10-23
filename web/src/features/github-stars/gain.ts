import type { StarsPoint } from './githubStarsApi'

export type GainStats = {
  /** Net stars in the newest bucket: the current hour or day so far. */
  current: number
  /** Net stars in the bucket before it, when there is one. */
  previous?: number
  /** Mean net stars per bucket over the whole range. */
  average: number
  /** The bucket with the most net stars, when any bucket gained. */
  peak?: { bucket: string; net: number }
}

/** Gain figures for the shown range, in the range's own bucket size (hours or days). */
export function gainStats(points: StarsPoint[]): GainStats | undefined {
  if (points.length === 0) return undefined
  const total = points.reduce((sum, point) => sum + point.net, 0)
  let peak: GainStats['peak']
  for (const point of points) {
    if (point.net > 0 && (!peak || point.net > peak.net)) peak = { bucket: point.bucket, net: point.net }
  }
  return {
    current: points[points.length - 1].net,
    previous: points.length > 1 ? points[points.length - 2].net : undefined,
    average: total / points.length,
    peak,
  }
}
